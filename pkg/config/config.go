package config

import (
	"os"
	"strings"
)

type Config struct {
	Addr        string
	DatabaseDSN string
	StorageDir  string // 文件内容存放的根目录
}

func Load() Config {
	return Config{
		Addr:        env("NETDISK_ADDR", ":1323"),
		DatabaseDSN: env("NETDISK_DSN", "data/db"),
		StorageDir:  env("NETDISK_STORAGE", "data/files"),
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
