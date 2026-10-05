package file

import (
	"archive/zip"
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
	"path"
	"path/filepath"
	"sort"
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

// 压缩文件夹
// 思路：前面实现过 collectSubtree，可以直接获取所有的子文件，但是这是扁平的，所以可以直接用这个算 etag
// 但是重建目录就需要搞清楚文件之间的关系，所以需要先把每个子文件夹对应的文件映射关系处理成 map，再递归遍历

// 返回值里的 wroteBody 告诉调用方响应体开始了没有
//   - false（还在查库阶段就失败了）：照常返回错误，Echo 会给出 500；
//   - true（已经开始写了）：状态码改不了了，调用方只能 panic(http.ErrAbortHandler)
//     把连接掐断，让客户端明确知道下载失败。
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
		return true, err // 故意不 Close：见函数注释
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

// write 递归写出一个目录及其子项。prefix 是它在 zip 里的路径。
func (t *zipTree) write(ctx context.Context, dir File, prefix string) error {
	// 目录自己也要占一条记录（名字以 / 结尾）：空目录全靠它才不会丢。
	// 最外层的目录也包括在内，这样解压出来是一整个文件夹，而不是散落一地。
	if prefix != "" {
		if _, err := t.zw.Create(prefix + "/"); err != nil {
			return fmt.Errorf("写入目录项 %s: %w", prefix, err)
		}
	}

	for _, f := range t.children[dir.ID] {
		// 客户端断开（或按了取消）就别再压了，不然白读一大片磁盘。
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
		// 记录还在、磁盘上没了，跳过它，只是少一个文件；总比整个下载失败好。
		t.log.Error("打包时跳过打不开的对象",
			"err", err, "file_id", f.ID, "storage_key", f.StorageKey, "name", f.Name)
		return nil
	}
	defer src.Close()

	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	// 已经压过的格式再 Deflate 一次基本不省空间。
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
