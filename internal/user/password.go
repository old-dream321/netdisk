package user

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// maxPasswordBytes 是 bcrypt 的硬限制：超过 72 字节的部分会被静默丢弃。
// 与其让用户以为那截内容起了作用，不如在校验阶段就明确拒绝。
const maxPasswordBytes = 72

// hashPassword 用 bcrypt 生成密码摘要。
// bcrypt 自带随机盐（所以同一密码每次的结果都不同），并把成本参数写进
// 摘要串里，将来调高成本也不影响老数据的校验。
//
// 因此：写库用 hashPassword，比对只能走 verifyPassword——绝不能把两次
// hashPassword 的结果拿来比较。
func hashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("生成密码摘要: %w", err)
	}
	return string(hash), nil
}

// verifyPassword 校验明文与摘要是否匹配。bcrypt 内部使用恒定时间比较，
// 不会因为"第几个字节开始不同"而泄露信息。
func verifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
