package user

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt 的限制：超过 72 字节的部分会被静默丢弃。
const maxPasswordBytes = 72

func hashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("生成密码摘要: %w", err)
	}
	return string(hash), nil
}

// 恒定时间比较
func verifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
