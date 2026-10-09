package file

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"netdisk/internal/user"
)

func removefile(ctx context.Context, db *gorm.DB, ownerID uint64, fileid uint64) error {
	file, err := gorm.G[File](db).Where("id = ?", fileid).First(ctx)
	if err != nil {
		return err
	}

	ids := []uint64{file.ID}
	if file.Type == TypeDir {
		descendants, err := collectSubtree(ctx, db, ownerID, file.ID)
		if err != nil {
			return fmt.Errorf("查询子项: %w", err)
		}
		for _, f := range descendants {
			ids = append(ids, f.ID)
		}
	}

	if _, err := gorm.G[map[string]any](db).Table("files").
		Where("id IN ? AND status <> ?", ids, StatusTrash).
		Updates(ctx, map[string]any{"status": StatusTrash, "deleted_at": time.Now()}); err != nil {
		return fmt.Errorf("更新状态: %w", err)
	}
	return nil
}

// 把 id 及所有"还在回收站里"的子孙恢复成正常状态，返回恢复的条数。
func restoreTree(ctx context.Context, db *gorm.DB, ownerID, id uint64) (int, error) {
	descendants, err := collectSubtree(ctx, db, ownerID, id)
	if err != nil {
		return 0, err
	}

	ids := make([]uint64, 0, len(descendants)+1)
	ids = append(ids, id)
	for _, f := range descendants {
		ids = append(ids, f.ID)
	}

	n, err := gorm.G[map[string]any](db).Table("files").
		Where("id IN ? AND owner_id = ? AND status = ?", ids, ownerID, StatusTrash).
		Updates(ctx, map[string]any{"status": StatusNormal, "deleted_at": nil})
	if err != nil {
		return 0, fmt.Errorf("恢复状态: %w", err)
	}
	return n, nil
}

// 彻底删除 id 及其子孙：先删数据库记录，再清理不再被引用的磁盘对象。
func purgeTree(ctx context.Context, db *gorm.DB, ownerID uint64, root File, storageDir string) error {
	descendants, err := collectSubtree(ctx, db, ownerID, root.ID)
	if err != nil {
		return err
	}

	ids := make([]uint64, 0, len(descendants)+1)
	ids = append(ids, root.ID)
	keys := make([]string, 0, len(descendants)+1)
	if root.StorageKey != "" {
		keys = append(keys, root.StorageKey)
	}
	for _, f := range descendants {
		ids = append(ids, f.ID)
		if f.StorageKey != "" {
			keys = append(keys, f.StorageKey)
		}
	}

	if _, err := gorm.G[File](db).
		Where("id IN ? AND owner_id = ?", ids, ownerID).
		Delete(ctx); err != nil {
		return fmt.Errorf("删除记录: %w", err)
	}

	// 如果没有引用再删
	for _, key := range keys {
		refs, err := gorm.G[File](db).Where("storage_key = ?", key).Count(ctx, "*")
		if err != nil {
			return fmt.Errorf("统计对象引用: %w", err)
		}
		if refs > 0 {
			continue
		}
		if err := os.Remove(ObjectPath(storageDir, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除对象文件: %w", err)
		}
	}

	if _, err := user.RefreshUsed(ctx, db, ownerID); err != nil {
		slog.Warn("刷新用户用量失败", "err", err, "owner_id", ownerID)
	}
	return nil
}
