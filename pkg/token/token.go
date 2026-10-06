package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const CookieName = "netdisk_session"

var ErrInvalid = errors.New("登录凭证无效或已过期")

type claims struct {
	UserID uint64 `json:"uid"`
	jwt.RegisteredClaims
}

type Signer struct {
	secret []byte
	ttl    time.Duration
}

func New(secret string, ttl time.Duration) (*Signer, error) {
	if secret == "" {
		return nil, errors.New("token 密钥不能为空")
	}
	if ttl <= 0 {
		return nil, errors.New("token 有效期必须是正数")
	}
	return &Signer{secret: []byte(secret), ttl: ttl}, nil
}

func (s *Signer) TTL() time.Duration { return s.ttl }

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
