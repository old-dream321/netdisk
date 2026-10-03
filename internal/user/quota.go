package user

// 配额统计与校验。
//
// 两条规则先定下来：
//  1. 用量用 SUM 实时算，不依赖 users.used 那个缓存字段——配额校验必须准确，
//     缓存会漂移；实时算只有一次索引查询的成本（owner_id 上有索引）。
//     users.used 仍然维护，但只作为"给 /me 显示"的缓存，随时可以重算。
//  2. 回收站里的文件仍然占用配额。被删除的对象还在磁盘上，否则谁都能靠
//     "删进回收站"来无限占用空间。只有彻底删除才释放。

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var ErrQuotaExceeded = errors.New("空间不足")

const statusDeleted int8 = 3

func Usage(ctx context.Context, db *gorm.DB, userID uint64) (int64, error) {
	var used int64
	err := db.WithContext(ctx).
		Table("files").
		Where("owner_id = ? AND status <> ?", userID, statusDeleted).
		Select("COALESCE(SUM(size), 0)").
		Scan(&used).Error
	if err != nil {
		return 0, fmt.Errorf("统计已用空间: %w", err)
	}
	return used, nil
}

// EnsureCapacity 检查"再放进 addSize 字节"会不会超配额。
// 配额 <= 0 视为不限量（方便给某些账号放开）。
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

// RefreshUsed 把实时用量写回 users.used，返回最新用量。
// 上传和彻底删除之后各调用一次；/me 也会调用一次，让这个缓存自愈。
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

// formatBytes 把字节数变成人看得懂的形式（只用于错误提示）。
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
