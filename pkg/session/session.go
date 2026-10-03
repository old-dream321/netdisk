// Package session 负责登录态的存取。
//
// 目前是"用 JSON 文件当 Redis"的临时实现：进程内 map + 落盘持久化。
// 接入 Redis 时只需再写一个实现同一接口的类型，调用方（user 包）不用改。
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	LoginCookieName = "netdisk_session"
)

// ErrNotFound 表示 token 不存在、已过期或已被登出。
var ErrNotFound = errors.New("会话不存在或已过期")

// Session 是登录态的载荷。真实项目里常存 user_id、角色、登录时间等。
type Session struct {
	UserID    uint64    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store 是登录态的存储抽象。换 Redis 就是换这个接口的实现。
type Store interface {
	// Create 生成新 token 并写入一条会话，有效期由 store 自己的 ttl 决定。
	Create(ctx context.Context, userID uint64) (token string, sess Session, err error)
	// Get 返回未过期的会话；不存在或已过期时返回 ErrNotFound。
	Get(ctx context.Context, token string) (Session, error)
	// Delete 注销会话；token 不存在时不算错误（保证幂等）。
	Delete(ctx context.Context, token string) error
}

// NewToken 生成 32 字节随机 token（base64url 后 43 个字符）。
// 用 crypto/rand 而不是 math/rand：后者可预测，token 会被猜出来。
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机 token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// fileStore 是 Store 的文件实现。
type fileStore struct {
	mu    sync.Mutex
	path  string
	ttl   time.Duration
	items map[string]Session
}

// NewFileStore 打开（或创建）path 处的会话文件，ttl 是会话有效期。
func NewFileStore(path string, ttl time.Duration) (Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建会话目录: %w", err)
		}
	}
	s := &fileStore{path: path, ttl: ttl, items: map[string]Session{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *fileStore) Create(_ context.Context, userID uint64) (string, Session, error) {
	token, err := NewToken()
	if err != nil {
		return "", Session{}, err
	}
	sess := Session{UserID: userID, ExpiresAt: time.Now().Add(s.ttl)}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[token] = sess
	if err := s.dump(); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

func (s *fileStore) Get(_ context.Context, token string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.items[token]
	if !ok {
		return Session{}, ErrNotFound
	}
	// 过期即视为不存在，顺手清掉，避免文件越攒越大。
	if time.Now().After(sess.ExpiresAt) {
		delete(s.items, token)
		_ = s.dump()
		return Session{}, ErrNotFound
	}
	return sess, nil
}

func (s *fileStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[token]; !ok {
		return nil // 幂等：重复登出不是错误
	}
	delete(s.items, token)
	return s.dump()
}

// load 读入已有会话；文件不存在时按"全新开始"处理。
func (s *fileStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取会话文件: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &s.items); err != nil {
		return fmt.Errorf("解析会话文件: %w", err)
	}
	return nil
}

// dump 是调用方持有锁时调用的：先清理过期项，再整体覆盖写。
// 用"临时文件 + rename"保证写到一半崩溃也不会留下半个 JSON 文件。
func (s *fileStore) dump() error {
	now := time.Now()
	for token, sess := range s.items {
		if now.After(sess.ExpiresAt) {
			delete(s.items, token)
		}
	}

	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	// token 等同于凭据，文件权限要收紧到只有属主可读写。
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("设置文件权限: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("写入会话: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("保存会话文件: %w", err)
	}
	return nil
}
