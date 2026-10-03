package router

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/internal/file"
	"netdisk/internal/user"
	"netdisk/pkg/session"
)

type Deps struct {
	DB         *gorm.DB
	Sessions   session.Store
	StorageDir string // 对象存储根目录（来自配置文件）
}

// New 组装全部路由。各业务模块只负责声明自己的端点，
// 这里统一决定它们挂在哪个前缀下。
func New(deps Deps) *echo.Echo {
	e := echo.New()

	e.GET("/healthz", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	api := e.Group("/api")
	user.RegisterRoutes(api.Group("/users"), deps.DB, deps.Sessions)
	file.RegisterRoutes(api.Group("/files"), deps.DB, deps.Sessions, deps.StorageDir)

	return e
}
