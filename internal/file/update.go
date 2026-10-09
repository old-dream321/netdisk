package file

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"netdisk/pkg/httpx"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

var errParentInvalid = errors.New("父目录不存在或不是可用目录")

func checkParent(ctx context.Context, db *gorm.DB, ownerID, parentID uint64) error {
	if parentID == 0 {
		return nil
	}

	parent, err := gorm.G[File](db).Where("id = ?", parentID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errParentInvalid
		}
		return fmt.Errorf("查询父目录: %w", err)
	}
	if parent.OwnerID != ownerID || parent.Type != TypeDir || parent.Status != StatusNormal {
		return errParentInvalid
	}
	return nil
}

// 判断响应应该是400（请求体有问题）还是500（服务端有问题）
func writeParentCheckError(c *echo.Context, err error) error {
	if errors.Is(err, errParentInvalid) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return httpx.Fail(c, err, "校验父目录失败")
}

var (
	errMoveToSelf       = errors.New("不能把条目移动到它自己里面")
	errMoveToDescendant = errors.New("不能把目录移动到它自己的子目录里")
)

// 校验 newParentID 能不能作为 file 的新位置，防止成环
func checkMoveTarget(ctx context.Context, db *gorm.DB, ownerID uint64, file File, newParentID uint64) error {
	if err := checkParent(ctx, db, ownerID, newParentID); err != nil {
		return err
	}
	if newParentID == file.ID {
		return errMoveToSelf
	}
	if file.Type != TypeDir {
		return nil // 文件没有子孙，不可能成环
	}

	descendants, err := collectSubtree(ctx, db, ownerID, file.ID)
	if err != nil {
		return err
	}
	for _, d := range descendants {
		if d.ID == newParentID {
			return errMoveToDescendant
		}
	}
	return nil
}

func writeMoveError(c *echo.Context, err error) error {
	if errors.Is(err, errParentInvalid) || errors.Is(err, errMoveToSelf) || errors.Is(err, errMoveToDescendant) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return httpx.Fail(c, err, "校验移动目标失败")
}
