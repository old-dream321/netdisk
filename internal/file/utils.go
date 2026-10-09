package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

// ---------上传下载---------

// 拼接文件路径
func ObjectPath(dir, storageKey string) string {
	return filepath.Join(dir, filepath.FromSlash(storageKey))
}

// 生成下载响应的 Content-Disposition 值
func ContentDisposition(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// 提供下载响应
func ServeObject(c *echo.Context, storageDir string, f File) error {
	src, err := os.Open(ObjectPath(storageDir, f.StorageKey))
	if err != nil {
		c.Logger().Error("对象文件打开失败，记录与磁盘不一致",
			"err", err, "file_id", f.ID, "owner_id", f.OwnerID, "storage_key", f.StorageKey)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "读取文件内容失败"})
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		c.Logger().Error("读取对象文件信息失败",
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

// 保存文件到对象存储目录，返回存储 key、文件大小、sha256 摘要
func saveUpload(dir string, src io.Reader) (storageKey string, size int64, hash string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, "", fmt.Errorf("创建存储目录: %w", err)
	}

	// 写到临时文件里，可以防止使用过多内存，也可以防止写到一半就被中断。
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
	// 如果目标文件已存在，说明内容相同，直接丢掉临时文件即可
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

// 返回 rootID 下的所有子孙（不含 rootID 自己）。
func collectSubtree(ctx context.Context, db *gorm.DB, ownerID, rootID uint64) ([]File, error) {
	var out []File
	queue := []uint64{rootID}

	for i := 0; i < len(queue); i++ {
		children, err := gorm.G[File](db).
			Where("parent_id = ? AND owner_id = ?", queue[i], ownerID).
			Find(ctx)
		if err != nil {
			return nil, fmt.Errorf("查询 id=%d 的子项: %w", queue[i], err)
		}
		for _, child := range children {
			out = append(out, child)
			if child.Type == TypeDir {
				queue = append(queue, child.ID)
			}
		}
	}
	return out, nil
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

const maxNameBytes = 255

func validateName(name string) error {
	if name == "" {
		return errors.New("名字不能为空")
	}
	if name == "." || name == ".." {
		return errors.New("名字不能是 . 或 ..")
	}
	if len(name) > maxNameBytes {
		return fmt.Errorf("名字过长（最多 %d 字节）", maxNameBytes)
	}
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			return errors.New(`名字不能包含 / 或 \`)
		case r < 0x20 || r == 0x7f:
			return fmt.Errorf("名字不能包含控制字符（%q）", r)
		}
	}
	return nil
}

// 取路径最后一段
func sanitizeUploadName(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	return raw
}
