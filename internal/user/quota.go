package user

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var ErrQuotaExceeded = errors.New("空间不足")

func Usage(ctx context.Context, db *gorm.DB, userID uint64) (int64, error) {
	var used int64
	err := db.WithContext(ctx).
		Table("files").
		Where("owner_id = ?", userID).
		Select("COALESCE(SUM(size), 0)").
		Scan(&used).Error
	if err != nil {
		return 0, fmt.Errorf("统计已用空间: %w", err)
	}
	return used, nil
}

// 检查"再放进 addSize 字节"会不会超配额。
// 配额 <= 0 视为不限量
func EnsureCapacity(ctx context.Context, db *gorm.DB, userID uint64, addSize int64) error {
	u, err := gorm.G[User](db).Where("id = ?", userID).First(ctx)
	if err != nil {
		return fmt.Errorf("查询用户: %w", err)
	}
	if u.Quota <= 0 {
		return nil
	}

	used, err := Usage(ctx, db, userID)
	if err != nil {
		return err
	}
	if used+addSize > u.Quota {
		return fmt.Errorf("%w：已用 %s / 配额 %s，本次需要 %s",
			ErrQuotaExceeded, formatBytes(used), formatBytes(u.Quota), formatBytes(addSize))
	}
	return nil
}

// 刷新用量
func RefreshUsed(ctx context.Context, db *gorm.DB, userID uint64) (int64, error) {
	used, err := Usage(ctx, db, userID)
	if err != nil {
		return 0, err
	}
	if _, err := gorm.G[User](db).
		Where("id = ?", userID).
		Update(ctx, "used", used); err != nil {
		return 0, fmt.Errorf("刷新用量: %w", err)
	}
	return used, nil
}

// 把字节数变成人看得懂的形式（只用于错误提示）
func formatBytes(n int64) string {
	value := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if value < 1024 || unit == "TB" {
			if unit == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", value, unit)
		}
		value /= 1024
	}
	return fmt.Sprintf("%d B", n)
}
