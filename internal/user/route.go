package user

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

func RegisterRoutes(g *echo.Group, db *gorm.DB) {
	g.POST("", register(db))
	g.POST("/login", login(db))
}

func register(_ *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return c.JSON(http.StatusNotImplemented, map[string]string{
			"error": "注册接口尚未实现",
		})
	}
}

func login(_ *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return c.JSON(http.StatusNotImplemented, map[string]string{
			"error": "登录接口尚未实现",
		})
	}
}
