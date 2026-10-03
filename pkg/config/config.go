package config

import (
	"log"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr        string
	DatabaseDSN string
	StorageDir  string        // 文件内容存放的根目录
	SessionFile string        // 会话存储文件（Redis 的临时替身）
	SessionTTL  time.Duration // 登录态有效期
}

func Load() Config {
	return Config{
		Addr:        env("NETDISK_ADDR", ":1323"),
		DatabaseDSN: env("NETDISK_DSN", "data/db"),
		StorageDir:  env("NETDISK_STORAGE", "data/files"),
		SessionFile: env("NETDISK_SESSION_FILE", "data/sessions.json"),
		SessionTTL:  envDuration("NETDISK_SESSION_TTL", 7*24*time.Hour),
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// envDuration 解析 "30m" / "168h" 这类时长；写错了就退回默认值并打印提示，
// 而不是让服务起不来。
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("环境变量 %s=%q 不是合法时长，使用默认值 %s", key, raw, fallback)
		return fallback
	}
	return d
}
