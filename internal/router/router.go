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
	"netdisk/pkg/session"
)

type Deps struct {
	DB         *gorm.DB
	Sessions   session.Store
	StorageDir string // 对象存储根目录（来自配置文件）
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
	user.RegisterRoutes(api.Group("/users"), deps.DB, deps.Sessions)
	file.RegisterRoutes(api.Group("/files"), deps.DB, deps.Sessions, deps.StorageDir)

	// 分享分两组：需要登录的挂 /api/shares
	share.RegisterRoutes(api.Group("/shares"), deps.DB, deps.Sessions)
	share.RegisterPublicRoutes(e, deps.DB, deps.StorageDir)

	return e
}

// 构造请求日志中间件。
//
// 用 RequestLoggerWithConfig 而不是现成的 RequestLogger()，是因为有三处要改：
//
//  1. Skipper：/s/:token —— 那个 token 是访问分享的唯一凭证，不能明文进日志文件。
//  2. 只要六个字段。默认那一套里的 host / user_agent / request_id /
//     bytes_in / bytes_out 用不上
//  3. latency 记成 "25.4µs" 比默认的纳秒整数（25402）好读
func newRequestLogger() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper: func(c *echo.Context) bool {
			// c.Path() 返回的是匹配到的路由模板（"/s/:token"），不是实际 URI，
			// 所以不同时不同 token 都能命中。
			return c.Path() == "/s/:token"
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
