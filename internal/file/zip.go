package file

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

// 压缩文件夹
// 思路：前面实现过 collectSubtree，可以直接获取所有的子文件，但是这是扁平的，所以可以直接用这个算 etag
// 但是重建目录就需要搞清楚文件之间的关系，所以需要先把每个子文件夹对应的文件映射关系处理成 map，再递归遍历

// 如果中途失败，返回值里的 wroteBody 告诉调用方响应体开始了没有，不能返回一个不完整的zip
//   - false（还在查库阶段就失败了）：照常返回错误，Echo 会给出 500；
//   - true（已经开始写了）：状态码改不了了，调用方只能 panic(http.ErrAbortHandler) 把连接掐断，让客户端明确知道下载失败。
func StreamZipDir(c *echo.Context, db *gorm.DB, ownerID uint64, dir File, storageDir string) (wroteBody bool, err error) {
	ctx := c.Request().Context()

	// 这样只用查一次库，也可以直接算出etag，但是重建目录树确实麻烦
	descendants, err := collectSubtree(ctx, db, ownerID, dir.ID)
	if err != nil {
		return false, fmt.Errorf("查询目录内容: %w", err)
	}

	alive := make([]File, 0, len(descendants))
	for _, f := range descendants {
		if f.Status == StatusNormal {
			alive = append(alive, f)
		}
	}

	// 内容指纹：只跟“这棵树当前长什么样”有关，和响应生成时间无关，所以稳定。
	etag := subtreeFingerprint(dir, alive)

	h := c.Response().Header()
	h.Set("ETag", etag) // 必须在 304 分支之前设，304 也要带它

	// 条件请求：内容没变就直接 304, echo 并不自带这个功能
	if match := c.Request().Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		return false, c.NoContent(http.StatusNotModified)
	}

	h.Set(echo.HeaderContentType, "application/zip")
	if cd := ContentDisposition(dir.Name + ".zip"); cd != "" {
		h.Set(echo.HeaderContentDisposition, cd)
	}
	// 不设 Content-Length（边压边发，事先不知道），也不设 Accept-Ranges，做不到
	// no-cache 而不是 no-store：允许存，但每次必须先来问一句。
	h.Set(echo.HeaderCacheControl, "private, no-cache")

	// 从这里开始，响应头已经发出去了，后面只能靠“掐断连接”表达失败。
	c.Response().WriteHeader(http.StatusOK)

	zw := zip.NewWriter(c.Response())
	tree := &zipTree{
		zw:         zw,
		children:   groupByParent(alive),
		storageDir: storageDir,
		log:        c.Logger(),
	}
	if err := tree.write(ctx, dir, dir.Name); err != nil {
		return true, err
	}
	if err := zw.Close(); err != nil {
		return true, fmt.Errorf("写入 zip 中央目录: %w", err)
	}
	return true, nil
}

// 用结构体，递归时方便。
type zipTree struct {
	zw         *zip.Writer
	children   map[uint64][]File // parent_id -> 子项（每层已按名字排序）
	storageDir string
	log        *slog.Logger
}

// 递归写出一个目录及其子项
func (t *zipTree) write(ctx context.Context, dir File, prefix string) error {
	if prefix != "" {
		// 这一步创建可以保留空目录
		if _, err := t.zw.Create(prefix + "/"); err != nil {
			return fmt.Errorf("写入目录项 %s: %w", prefix, err)
		}
	}

	for _, f := range t.children[dir.ID] {
		if err := ctx.Err(); err != nil {
			return err
		}

		name := joinZipPath(prefix, f.Name)
		if f.Type == TypeDir {
			if err := t.write(ctx, f, name); err != nil {
				return err
			}
			continue
		}
		if err := t.writeFile(ctx, f, name); err != nil {
			return err
		}
	}
	return nil
}

// 写一个文件条目。
func (t *zipTree) writeFile(_ context.Context, f File, name string) error {
	// 必须先把内容打开成功，再写 header
	src, err := os.Open(ObjectPath(t.storageDir, f.StorageKey))
	if err != nil {
		// 记录还在、磁盘上没了，跳过它
		t.log.Error("打包时跳过打不开的对象",
			"err", err, "file_id", f.ID, "storage_key", f.StorageKey, "name", f.Name)
		return nil
	}
	defer src.Close()

	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	// 已经压过的格式不用再 Deflate
	if alreadyCompressed(f.Name) {
		hdr.Method = zip.Store
	}
	w, err := t.zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("写入条目 %s: %w", name, err)
	}
	if _, err := io.Copy(w, src); err != nil {
		return fmt.Errorf("压缩 %s: %w", name, err)
	}
	return nil
}

// 把扁平的子树整理成 parent_id -> 子项 的映射，每层按名字排序，保证下载的一致
func groupByParent(files []File) map[uint64][]File {
	m := make(map[uint64][]File, len(files))
	for _, f := range files {
		m[f.ParentID] = append(m[f.ParentID], f)
	}
	for id := range m {
		children := m[id]
		sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	}
	return m
}

// 算出“这棵目录树当前的样子”的指纹，用作 ETag，避免重复下载
// 使用确定的顺序，只加确定的内容
func subtreeFingerprint(root File, descendants []File) string {
	sorted := make([]File, len(descendants))
	copy(sorted, descendants)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	h := sha256.New()
	fmt.Fprintf(h, "d\x00%d\x00%s\x00", root.ID, root.Name)
	for _, f := range sorted {
		// Hash 对目录是空串，无所谓：id/名字/类型变了也会反映出来。
		fmt.Fprintf(h, "%d\x00%s\x00%d\x00%s\x00%d\n", f.ID, f.Name, f.Type, f.Hash, f.Size)
	}
	return `"` + hex.EncodeToString(h.Sum(nil)) + `"`
}

func etagMatches(header, etag string) bool {
	// 可能有多个
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		// W/ 是弱校验前缀。这里用不到，但客户端可能带上，宽松处理。
		if part == "*" || part == etag || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}

func joinZipPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// 判断这个扩展名是不是已经压过了。
func alreadyCompressed(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".avif",
		".mp4", ".mkv", ".mov", ".webm", ".avi",
		".mp3", ".aac", ".flac", ".ogg", ".opus",
		".zip", ".gz", ".bz2", ".xz", ".7z", ".rar", ".zst":
		return true
	}
	return false
}
