// Package token 负责登录凭证的签发与校验（JWT）。
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// CookieName 是承载 token 的 cookie 名。登录时下发、鉴权时读取。
const CookieName = "netdisk_session"

var ErrInvalid = errors.New("登录凭证无效或已过期")

type claims struct {
	UserID uint64 `json:"uid"`
	jwt.RegisteredClaims
}

// Signer 用同一个密钥签发和校验 token。
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// New 用 HMAC 密钥构造 Signer。secret 不能为空。
func New(secret string, ttl time.Duration) (*Signer, error) {
	if secret == "" {
		return nil, errors.New("token 密钥不能为空")
	}
	if ttl <= 0 {
		return nil, errors.New("token 有效期必须是正数")
	}
	return &Signer{secret: []byte(secret), ttl: ttl}, nil
}

// TTL 返回 token 的有效期，调用方用它设置 cookie 的 Expires。
func (s *Signer) TTL() time.Duration { return s.ttl }

// Issue 为 userID 签发一个 token，同时返回它的过期时间。
func (s *Signer) Issue(userID uint64) (string, time.Time, error) {
	now := time.Now()
	expires := now.Add(s.ttl)

	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		UserID: userID,

		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
	})

	signed, err := t.SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发 token: %w", err)
	}
	return signed, expires, nil
}

// 校验 token 并返回其中的用户 ID。
func (s *Signer) Verify(raw string) (uint64, error) {
	var c claims

	_, err := jwt.ParseWithClaims(raw, &c, func(*jwt.Token) (any, error) {
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return 0, ErrInvalid
	}
	if c.UserID == 0 {
		return 0, ErrInvalid
	}
	return c.UserID, nil
}
