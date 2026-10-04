package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/pkg/httpx"
)

// ObjectPath 把对象 key 映射成存储根目录下的真实路径。
// 存储目录由调用方传入（来自配置文件），不再在这里偷偷读一次配置。
func ObjectPath(dir, storageKey string) string {
	return filepath.Join(dir, filepath.FromSlash(storageKey))
}

// ContentDisposition 生成下载响应的 Content-Disposition 值。
// 交给标准库 mime.FormatMediaType：非 ASCII 文件名会按 RFC 2231/5987 编码成
// 扩展形式（filename* 参数），引号、反斜杠等字符也由它负责转义；
// 值无法表示时返回空串，此时调用方应当跳过设置该头。
func ContentDisposition(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// 提供下载响应
func ServeObject(c *echo.Context, storageDir string, f File) error {
	src, err := os.Open(ObjectPath(storageDir, f.StorageKey))
	if err != nil {
		slog.Error("对象文件打开失败，记录与磁盘不一致",
			"err", err, "file_id", f.ID, "owner_id", f.OwnerID, "storage_key", f.StorageKey)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "读取文件内容失败"})
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		slog.Error("读取对象文件信息失败",
			"err", err, "file_id", f.ID, "storage_key", f.StorageKey)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "读取文件信息失败"})
	}

	h := c.Response().Header()
	if cd := ContentDisposition(f.Name); cd != "" {
		h.Set(echo.HeaderContentDisposition, cd)
	}
	http.ServeContent(c.Response(), c.Request(), f.Name, info.ModTime(), src)
	return nil
}

// 保存文件到对象存储目录，返回存储 key、文件大小、sha256 摘要。
func saveUpload(dir string, src io.Reader) (storageKey string, size int64, hash string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, "", fmt.Errorf("创建存储目录: %w", err)
	}

	// 写到临时文件里，可以防止使用过多内存（io.Copy 会尽量用 32KB 缓冲），也可以防止写到一半就被中断。
	tmp, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return "", 0, "", fmt.Errorf("创建临时文件: %w", err)
	}
	// 就算中途断联也能清理，而不会产生不完整的对象文件。
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	sum := sha256.New()
	if size, err = io.Copy(io.MultiWriter(tmp, sum), src); err != nil {
		return "", 0, "", fmt.Errorf("写入文件内容: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", 0, "", fmt.Errorf("关闭临时文件: %w", err)
	}

	hash = hex.EncodeToString(sum.Sum(nil))
	storageKey = filepath.ToSlash(filepath.Join(hash[:2], hash))

	dst := ObjectPath(dir, storageKey)
	// 如果目标文件已存在，说明内容相同，直接丢掉临时文件即可。
	if _, statErr := os.Stat(dst); statErr == nil {
		return storageKey, size, hash, nil
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, "", fmt.Errorf("创建对象目录: %w", err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return "", 0, "", fmt.Errorf("保存对象: %w", err)
	}
	return storageKey, size, hash, nil
}

func parseOptionalUint(raw string, fallback uint64) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

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

// 把 checkParent 的错误翻成响应，校验不通过是调用方的问题(400)，查询本身失败是服务端的锅(500)。
func writeParentCheckError(c *echo.Context, err error) error {
	if errors.Is(err, errParentInvalid) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return httpx.Fail(c, err, "校验父目录失败")
}

// 判断同名
func nameTaken(ctx context.Context, db *gorm.DB, ownerID, parentID uint64, name string, excludeID uint64) (bool, error) {
	q := gorm.G[File](db).
		Where("owner_id = ? AND parent_id = ? AND name = ? AND status = ?",
			ownerID, parentID, name, StatusNormal)
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}

	n, err := q.Count(ctx, "*")
	if err != nil {
		return false, fmt.Errorf("检查同名条目: %w", err)
	}
	return n > 0, nil
}

var errNameTaken = errors.New("同级下已存在同名文件或目录")

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

// 把移动目标的校验错误翻成响应：都是调用方输入的问题(400)，
// 查询本身失败才是服务端的锅(500)。
func writeMoveError(c *echo.Context, err error) error {
	if errors.Is(err, errParentInvalid) || errors.Is(err, errMoveToSelf) || errors.Is(err, errMoveToDescendant) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return httpx.Fail(c, err, "校验移动目标失败")
}
