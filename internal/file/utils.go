package file

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"netdisk/pkg/config"
)

// storageDir 是对象存储的根目录，进程启动时读取一次配置。
var storageDir = config.Load().StorageDir

// objectPath 把对象 key 映射成存储根目录下的真实路径。
func objectPath(storageKey string) string {
	return filepath.Join(storageDir, filepath.FromSlash(storageKey))
}

// contentDisposition 生成下载响应的 Content-Disposition 值。
// 交给标准库 mime.FormatMediaType：非 ASCII 文件名会按 RFC 2231/5987 编码成
// 扩展形式（filename* 参数），引号、反斜杠等字符也由它负责转义；
// 值无法表示时返回空串，此时调用方应当跳过设置该头。
func contentDisposition(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// saveUpload 把上传内容写入对象存储，返回对象 key、字节数以及内容的 sha256。
// key 采用「hash 前两位 / 完整 hash」的两级结构，内容相同的文件天然去重。
func saveUpload(src io.Reader) (storageKey string, size int64, hash string, err error) {
	if err = os.MkdirAll(storageDir, 0o755); err != nil {
		return "", 0, "", fmt.Errorf("创建存储目录: %w", err)
	}

	tmp, err := os.CreateTemp(storageDir, "upload-*")
	if err != nil {
		return "", 0, "", fmt.Errorf("创建临时文件: %w", err)
	}
	// 成功时文件已被 rename 走，这里只是兜底清理失败残留。
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

	dst := objectPath(storageKey)
	// 对象已存在说明同内容之前传过，直接复用，省掉一次 write；
	// 失败时（不存在或不可读）就当没这回事，继续走下面的正常写入。
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
