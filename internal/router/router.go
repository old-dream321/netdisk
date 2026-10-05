package router

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"gorm.io/gorm"

	"netdisk/internal/file"
	"netdisk/internal/share"
	"netdisk/internal/user"
	"netdisk/pkg/token"
)

type Deps struct {
	DB         *gorm.DB
	Signer     *token.Signer // 登录凭证的签发/校验器
	StorageDir string        // 对象存储根目录（来自配置文件）
	Logger     *slog.Logger
}

// 组装全部路由
func New(deps Deps) *echo.Echo {
	e := echo.New()
	if deps.Logger != nil {
		// 请求日志中间件写的是 c.Logger()，也就是 e.Logger，换掉它就够了。
		e.Logger = deps.Logger
	}

	// 中间件顺序：logger 在外、Recover 在内, 保证请求日志能记录到 panic
	e.Use(newRequestLogger())
	e.Use(middleware.Recover())

	e.GET("/healthz", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	api := e.Group("/api")
	user.RegisterRoutes(api.Group("/users"), deps.DB, deps.Signer)
	file.RegisterRoutes(api.Group("/files"), deps.DB, deps.Signer, deps.StorageDir)

	// 分享分两组：需要登录的挂 /api/shares
	share.RegisterRoutes(api.Group("/shares"), deps.DB, deps.Signer)
	share.RegisterPublicRoutes(e, deps.DB, deps.StorageDir)

	return e
}

// 请求日志中间件。
func newRequestLogger() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper: func(c *echo.Context) bool {
			// 防止分享token泄露
			switch c.Path() {
			case "/s/:token", "/s/:token/*":
				return true
			}
			return false
		},
		LogMethod:   true,
		LogURI:      true,
		LogStatus:   true,
		LogLatency:  true,
		LogRemoteIP: true,
		// 让错误先走全局错误处理器写出响应，我们才能记到最终状态码。
		HandleError: true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			level, msg := slog.LevelInfo, "REQUEST"
			if v.Error != nil {
				level, msg = slog.LevelError, "REQUEST_ERROR"
			}

			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("uri", v.URI),
				slog.Int("status", v.Status),
				slog.String("latency", v.Latency.String()),
				slog.String("remote_ip", v.RemoteIP),
			}
			if v.Error != nil {
				attrs = append(attrs, slog.String("error", v.Error.Error()))
			}

			c.Logger().LogAttrs(context.Background(), level, msg, attrs...)
			return nil
		},
	})
}
