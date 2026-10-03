package file

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

const (
	// sweepBatch 单批处理的条目数。
	sweepBatch = 100
	// sweepPause 批与批之间的停顿，同样是为了给正常请求让路。
	sweepPause = 50 * time.Millisecond
)

func StartJanitor(ctx context.Context, db *gorm.DB, storageDir string, ttl, interval time.Duration) {
	if interval <= 0 || ttl <= 0 {
		log.Printf("回收站清理未启动：保留时长 %s、扫描间隔 %s 都必须为正数", ttl, interval)
		return
	}

	log.Printf("回收站清理已启动：保留 %s，每 %s 扫描一次", ttl, interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("回收站清理已停止")
			return
		case <-ticker.C:
			n, err := SweepTrash(ctx, db, storageDir, ttl, sweepBatch)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					continue
				}
				log.Printf("回收站清理出错（本轮已清 %d 个条目）: %v", n, err)
				continue
			}
			if n > 0 {
				log.Printf("回收站清理完成，本轮清除 %d 个条目", n)
			}
		}
	}
}

// SweepTrash 清理一轮到期的回收站条目，返回清掉的条目数（只数子树根，见下）。
func SweepTrash(ctx context.Context, db *gorm.DB, storageDir string, ttl time.Duration, batch int) (int, error) {
	if batch <= 0 {
		batch = sweepBatch
	}
	cutoff := time.Now().Add(-ttl)

	total := 0
	for {
		// 只扫"子树根"：parent_id = 0，或者父目录当前不在回收站里
		// （很高级的SQL）
		candidates, err := gorm.G[File](db).
			Where(`status = ? AND deleted_at IS NOT NULL AND deleted_at < ?
				AND (parent_id = 0 OR NOT EXISTS (
					SELECT 1 FROM files p WHERE p.id = files.parent_id AND p.status = ?
				))`, StatusTrash, cutoff, StatusTrash).
			Order("deleted_at").
			Limit(batch).
			Find(ctx)
		if err != nil {
			return total, fmt.Errorf("查询过期回收站条目: %w", err)
		}
		if len(candidates) == 0 {
			return total, nil
		}

		removed := 0
		for _, f := range candidates {
			if err := purgeTree(ctx, db, f.OwnerID, f, storageDir); err != nil {
				// 一条失败不拖垮整轮：记下来继续，剩下的下一轮还会被扫到。
				log.Printf("清理条目 id=%d（owner_id=%d）失败: %v", f.ID, f.OwnerID, err)
				continue
			}
			removed++
		}
		total += removed

		// 一批下来一条都没清掉，说明这批现在都会失败；再查下去就是同一批数据
		if removed == 0 {
			log.Printf("回收站清理本轮无进展，剩余 %d 个条目留待下一轮", len(candidates))
			return total, nil
		}
		// 已经清完了
		if len(candidates) < batch {
			return total, nil
		}

		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(sweepPause):
		}
	}
}
