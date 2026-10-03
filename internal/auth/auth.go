package auth

// 本包只负责一件事：从 cookie 认出当前登录的用户，
// 并把用户 ID、会话 token 放进 echo 上下文，供业务 handler 取用。
import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"netdisk/pkg/httpx"
	"netdisk/pkg/session"
)

// 存进 echo 上下文的键。
const (
	userIDKey = "auth:user_id"
	tokenKey  = "auth:token"
)

// RequireLogin 是保护业务接口的中间件：没登录直接 401；
// 登录了就把用户 ID 和 token 放进上下文，后续 handler 用 UserID/Token 取。
func RequireLogin(sessions session.Store) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ck, err := c.Cookie(session.LoginCookieName)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "未登录"})
			}

			sess, err := sessions.Get(c.Request().Context(), ck.Value)
			if err != nil {
				if errors.Is(err, session.ErrNotFound) {
					return c.JSON(http.StatusUnauthorized, map[string]string{"error": "登录已过期，请重新登录"})
				}
				return httpx.Fail(c, err, "读取会话失败")
			}

			c.Set(userIDKey, sess.UserID)
			c.Set(tokenKey, ck.Value)
			return next(c)
		}
	}
}

// UserID 返回当前登录用户的 ID。
// 只能在 RequireLogin 之后的 handler 里调用；没经过中间件时返回 0，
// 会让查询条件变成 owner_id = 0 从而查不到任何数据，不会误放行。
func UserID(c *echo.Context) uint64 {
	id, _ := c.Get(userIDKey).(uint64)
	return id
}

// Token 返回当前会话 token（登出时要用它删掉对应的会话）。
func Token(c *echo.Context) string {
	token, _ := c.Get(tokenKey).(string)
	return token
}
