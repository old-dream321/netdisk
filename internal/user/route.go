package user

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/internal/auth"
	"netdisk/pkg/httpx"
	"netdisk/pkg/token"
)

const (
	// defaultQuota 新用户的默认配额：1GiB。
	defaultQuota int64 = 1 << 30
	// maxUsernameLen 用户名最大长度（按 rune 算，避免中文被按字节"缩短"）。
	maxUsernameLen = 64
)

func RegisterRoutes(g *echo.Group, db *gorm.DB, signer *token.Signer) {
	// 注册、登录是公开的。
	g.POST("/register", register(db))
	g.POST("/login", login(db, signer))

	// 登出、查当前用户需要登录：逐条挂上鉴权中间件。
	requireLogin := auth.RequireLogin(signer)
	g.POST("/logout", logout(), requireLogin)
	g.GET("/me", me(db), requireLogin)
}

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func validateUsername(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("用户名不能为空")
	}
	if trimmed != name {
		return errors.New("用户名首尾不能有空格")
	}
	if utf8.RuneCountInString(name) > maxUsernameLen {
		return fmt.Errorf("用户名不能超过 %d 个字符", maxUsernameLen)
	}
	return nil
}

func validateEmail(email string) error {
	if email == "" {
		return errors.New("邮箱不能为空")
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return errors.New("邮箱不能包含空格")
	}
	if !strings.Contains(email, "@") {
		return errors.New("邮箱格式不正确")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("密码不能超过 %d 字节", maxPasswordBytes)
	}
	return nil
}

func register(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		var req registerRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}

		if err := validateUsername(req.Username); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := validateEmail(req.Email); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := validatePassword(req.Password); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		sameName, err := gorm.G[User](db).Where("username = ?", req.Username).Count(ctx, "*")
		if err != nil {
			return httpx.Fail(c, err, "查询用户失败")
		}
		if sameName > 0 {
			return c.JSON(http.StatusConflict, map[string]string{"error": "用户名已被占用"})
		}

		sameEmail, err := gorm.G[User](db).Where("email = ?", req.Email).Count(ctx, "*")
		if err != nil {
			return httpx.Fail(c, err, "查询用户失败")
		}
		if sameEmail > 0 {
			return c.JSON(http.StatusConflict, map[string]string{"error": "邮箱已被注册"})
		}

		hash, err := hashPassword(req.Password)
		if err != nil {
			return httpx.Fail(c, err, "生成密码摘要失败")
		}

		u := User{
			Username: req.Username,
			Password: hash, // 只存摘要，明文不落库
			Email:    req.Email,
			Quota:    defaultQuota,
		}
		if err := gorm.G[User](db).Create(ctx, &u); err != nil {
			// 并发下可能有人在预检查之后抢先占了用户名/邮箱
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return c.JSON(http.StatusConflict, map[string]string{"error": "用户名或邮箱已被占用"})
			}
			return httpx.Fail(c, err, "创建用户失败")
		}

		return c.JSON(http.StatusCreated, u)
	}
}

func login(db *gorm.DB, signer *token.Signer) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		var req loginRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体解析失败"})
		}
		if req.Username == "" || req.Password == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "用户名和密码不能为空"})
		}

		u, err := gorm.G[User](db).Where("username = ?", req.Username).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errBadCredentials(c)
			}
			return httpx.Fail(c, err, "查询用户失败")
		}
		if !verifyPassword(u.Password, req.Password) {
			return errBadCredentials(c)
		}

		raw, expires, err := signer.Issue(uint64(u.ID))
		if err != nil {
			return httpx.Fail(c, err, "签发登录凭证失败")
		}
		setSessionCookie(c, raw, expires)

		return c.JSON(http.StatusOK, u)
	}
}

func errBadCredentials(c *echo.Context) error {
	return c.JSON(http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
}

func setSessionCookie(c *echo.Context, raw string, expires time.Time) {
	c.SetCookie(&http.Cookie{
		Name:     token.CookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true, // 禁止 JS 读取
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	})
}

// logout 只是让浏览器丢弃 cookie
func logout() echo.HandlerFunc {
	return func(c *echo.Context) error {
		// 让浏览器立刻丢弃 cookie：值清空 + MaxAge<0。
		c.SetCookie(&http.Cookie{
			Name:     token.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
		return c.NoContent(http.StatusNoContent)
	}
}

// 验证登录
func me(db *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		u, err := gorm.G[User](db).Where("id = ?", auth.UserID(c)).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "用户不存在"})
			}
			return httpx.Fail(c, err, "查询用户失败")
		}

		if used, err := RefreshUsed(ctx, db, uint64(u.ID)); err == nil {
			u.Used = used
		}

		return c.JSON(http.StatusOK, u)
	}
}
