// Package httpx 放各业务模块共用的 HTTP 响应小工具。
package httpx

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
)

// Fail 是所有"服务端内部错误"的统一出口。
//
// 唯一需要特殊对待的是 ctx 被取消或超时：那说明客户端已经断开，
// 此时往一个没人听的连接写 500 没有意义，还会掩盖真正的原因，
// 所以直接把原始错误交还给 Echo 的错误处理器（它会记进日志）。
func Fail(c *echo.Context, err error, msg string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": msg})
}
