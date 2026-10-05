package auth

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"netdisk/pkg/token"
)

const userIDKey = "auth:user_id"

// 校验 cookie 里的 JWT，通过后把用户 ID 放进上下文。
func RequireLogin(signer *token.Signer) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ck, err := c.Cookie(token.CookieName)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "未登录"})
			}

			userID, err := signer.Verify(ck.Value)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "登录已过期，请重新登录"})
			}

			c.Set(userIDKey, userID)
			return next(c)
		}
	}
}

func UserID(c *echo.Context) uint64 {
	id, _ := c.Get(userIDKey).(uint64)
	return id
}
