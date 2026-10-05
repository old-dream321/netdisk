package share

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"netdisk/internal/file"
	"netdisk/pkg/httpx"
)

var (
	errShareGone = errors.New("分享不存在或已失效")
	errPathGone  = errors.New("路径不存在")
	errBadPath   = errors.New("路径格式不正确")
)

const (
	// maxPathSegments 子路径最多几层
	maxPathSegments = 32
	// maxSegmentBytes 单层名字的长度上限，和 file 包的 maxNameBytes 对应。
	maxSegmentBytes = 255
)

// 解析的结果：分享本身 + 目标条目 + 从分享根到目标的路径。
type resolved struct {
	Share  Share
	Target file.File
	Path   []string
}

// 把 (token, 子路径) 解析成一个具体条目
func resolve(ctx context.Context, db *gorm.DB, token, subpath string) (resolved, error) {
	var out resolved

	// 先验证 token 本身
	sh, err := gorm.G[Share](db).Where("token = ?", token).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, errShareGone
		}
		return out, fmt.Errorf("查询分享: %w", err)
	}
	if sh.ExpiresAt != nil && !sh.ExpiresAt.After(time.Now()) {
		return out, errShareGone
	}
	out.Share = sh

	segs, err := splitSubpath(subpath)
	if err != nil {
		return out, err
	}

	// 分享的根自己。不加 status 过滤：根被删进回收站时，返回"分享已失效"，而不是让后面的循环给出一个含糊的"路径不存在"。
	cur, err := gorm.G[file.File](db).Where("id = ?", sh.FileID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, errShareGone
		}
		return out, fmt.Errorf("查询分享目标: %w", err)
	}
	if cur.Status != file.StatusNormal {
		return out, errShareGone
	}

	// 沿名字逐层下落
	for _, seg := range segs {
		if cur.Type != file.TypeDir {
			// 访问路径穿透了一个文件
			return out, errPathGone
		}
		child, err := gorm.G[file.File](db).
			Where("parent_id = ? AND name = ? AND owner_id = ? AND status = ?",
				cur.ID, seg, sh.OwnerID, file.StatusNormal).
			First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return out, errPathGone
			}
			return out, fmt.Errorf("查询子项 %q: %w", seg, err)
		}
		cur = child
	}

	out.Target = cur
	out.Path = segs
	return out, nil
}

// 把 c.Param("*") 切成分段并做基本校验。
func splitSubpath(raw string) ([]string, error) {
	// 去除两端斜杠
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return nil, nil
	}

	segs := strings.Split(raw, "/")
	if len(segs) > maxPathSegments {
		return nil, errBadPath
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, errBadPath
		}
		if len(s) > maxSegmentBytes {
			return nil, errBadPath
		}
	}
	return segs, nil
}

// 拼出某个位置的下载地址。
// 逐段转义：名字里的空格、#、? 不转义就会拼出一个坏 URL。
func downloadURL(token string, path []string) string {
	if len(path) == 0 {
		return "/s/" + token + "?download=1"
	}
	escaped := make([]string, len(path))
	for i, s := range path {
		escaped[i] = url.PathEscape(s)
	}
	return "/s/" + token + "/" + strings.Join(escaped, "/") + "?download=1"
}

// 把内部的 type 数字翻成对外的字符串。
func typeName(t int8) string {
	if t == file.TypeDir {
		return "dir"
	}
	return "file"
}

// 把 resolve 的错误翻成响应。
func writeResolveError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, errBadPath):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, errShareGone), errors.Is(err, errPathGone):
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		return httpx.Fail(c, err, "解析分享失败")
	}
}
