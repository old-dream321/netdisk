package user

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/pkg/httpx"
)

const (
	// defaultQuota 新用户的默认配额：1GiB。
	defaultQuota int64 = 1 << 30
	// minPasswordLen 密码最小长度（按字节算；纯 ASCII 时等价于字符数）。
	minPasswordLen = 8
	// maxUsernameLen 用户名最大长度（按 rune 算，避免中文被按字节"缩短"）。
	maxUsernameLen = 64
)

func RegisterRoutes(g *echo.Group, db *gorm.DB) {
	g.POST("", register(db))
	g.POST("/login", login(db))
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

// validateEmail 只做最基本的检查：非空、不含空白、包含 @。
// 完整的邮箱校验（RFC 5322）是个大坑，等真的需要发验证邮件时
// 再用 net/mail.ParseAddress，或者干脆靠"发信验证"来证明邮箱有效。
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

// validatePassword 长度上下限：下限是基本强度要求，上限来自 bcrypt 的硬限制。
func validatePassword(password string) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("密码至少 %d 位", minPasswordLen)
	}
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

		// 逐个字段校验并各自给出原因：合并成一句"用户名和密码不能为空"，
		// 调用方根本不知道自己到底漏了哪个。
		if err := validateUsername(req.Username); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := validateEmail(req.Email); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := validatePassword(req.Password); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		// 下面两个预检查只是为了告诉用户"具体是哪个字段重复了"。
		// 真正的唯一性保障是数据库唯一索引（见 Create 的错误分支）——
		// "先查后插"在并发下必然存在窗口，不能当唯一防线。
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
			// 长度已在校验阶段挡过，走到这里基本只会是真的故障。
			return httpx.Fail(c, err, "生成密码摘要失败")
		}

		u := User{
			Username: req.Username,
			Password: hash, // 只存摘要，明文不落库
			Email:    req.Email,
			Quota:    defaultQuota,
		}
		if err := gorm.G[User](db).Create(ctx, &u); err != nil {
			// 并发下可能有人在我们预检查之后抢先占了用户名/邮箱，
			// 此时唯一索引会报 ErrDuplicatedKey（靠 gorm.Config.TranslateError
			// 翻译而来）——这是最后一道防线，要翻成 409 而不是 500。
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return c.JSON(http.StatusConflict, map[string]string{"error": "用户名或邮箱已被占用"})
			}
			return httpx.Fail(c, err, "创建用户失败")
		}

		return c.JSON(http.StatusCreated, u)
	}
}

func login(_ *gorm.DB) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return c.JSON(http.StatusNotImplemented, map[string]string{
			"error": "登录接口尚未实现",
		})
	}
}
