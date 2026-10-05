// Package httpx 放各业务模块共用的 HTTP 响应小工具。
package httpx

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
)

func Fail(c *echo.Context, err error, msg string) error {

	h := c.Logger()
	// 记录模板，防止泄露token
	route := c.Path()

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		h.Debug(msg, "err", err, "route", route)
		return err
	}

	h.Error(msg, "err", err, "route", route)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": msg})
}
