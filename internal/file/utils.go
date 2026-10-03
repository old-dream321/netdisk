package file

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
)

// objectPath 把对象 key 映射成存储根目录下的真实路径。
// 存储目录由调用方传入（来自配置文件），不再在这里偷偷读一次配置。
func objectPath(dir, storageKey string) string {
	return filepath.Join(dir, filepath.FromSlash(storageKey))
}

// contentDisposition 生成下载响应的 Content-Disposition 值。
// 交给标准库 mime.FormatMediaType：非 ASCII 文件名会按 RFC 2231/5987 编码成
// 扩展形式（filename* 参数），引号、反斜杠等字符也由它负责转义；
// 值无法表示时返回空串，此时调用方应当跳过设置该头。
func contentDisposition(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
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

	dst := objectPath(dir, storageKey)
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
