package config

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultConfigFile = "config.yaml"

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Storage  StorageConfig  `yaml:"storage"`
	Session  SessionConfig  `yaml:"session"`
	Trash    TrashConfig    `yaml:"trash"`
	Log      LogConfig      `yaml:"log"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type StorageConfig struct {
	Dir string `yaml:"dir"`
}

type SessionConfig struct {
	File string        `yaml:"file"`
	TTL  time.Duration `yaml:"ttl"`
}

// TrashConfig 控制后台清理：进了回收站的条目躺够 TTL，就被彻底删除。
type TrashConfig struct {
	// 回收站里的保留时长
	TTL time.Duration `yaml:"ttl"`
	// 扫描间隔。
	Sweep time.Duration `yaml:"sweep"`
}

// LogConfig 控制日志级别。
type LogConfig struct {
	// Level 取 debug / info / warn / error（大小写不敏感），空值 = info。
	Level string `yaml:"level"`
}

// SlogLevel 把配置里的字符串转成 slog 级别。
func (c LogConfig) SlogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(c.Level)) {
	case "debug":
		return slog.LevelDebug
	case "", "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		log.Printf("无法识别的 log.level=%q，按 info 处理", c.Level)
		return slog.LevelInfo
	}
}

// Default 返回内置的默认配置。
func Default() Config {
	return Config{
		Server:   ServerConfig{Addr: ":8080"},
		Database: DatabaseConfig{DSN: "data"},
		Storage:  StorageConfig{Dir: "data/files"},
		Session:  SessionConfig{File: "data/sessions.json", TTL: 24 * time.Hour},
		Trash:    TrashConfig{TTL: 30 * 24 * time.Hour, Sweep: time.Hour},
		Log:      LogConfig{Level: "info"},
	}
}

func Load() (Config, error) {
	f, err := os.Open(DefaultConfigFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("打开配置文件 %s: %w", DefaultConfigFile, err)
		}

		cfg := Default()
		if err := saveConfig(cfg); err != nil {
			log.Printf("无法生成默认配置文件（将只使用内置默认值）: %v", err)
		} else {
			log.Printf("已生成默认配置文件 %s，修改后重启即可生效", DefaultConfigFile)
		}
		return cfg, nil
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件 %s: %w", DefaultConfigFile, err)
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	def := Default()
	if c.Server.Addr == "" {
		c.Server.Addr = def.Server.Addr
	}
	if c.Database.DSN == "" {
		c.Database.DSN = def.Database.DSN
	}
	if c.Storage.Dir == "" {
		c.Storage.Dir = def.Storage.Dir
	}
	if c.Session.File == "" {
		c.Session.File = def.Session.File
	}
	if c.Session.TTL <= 0 {
		log.Printf("session.ttl 未设置或不是正数，使用默认值 %s", def.Session.TTL)
		c.Session.TTL = def.Session.TTL
	}
	if c.Trash.TTL <= 0 {
		log.Printf("trash.ttl 未设置或不是正数，使用默认值 %s", def.Trash.TTL)
		c.Trash.TTL = def.Trash.TTL
	}
	if c.Trash.Sweep <= 0 {
		log.Printf("trash.sweep 未设置或不是正数，使用默认值 %s", def.Trash.Sweep)
		c.Trash.Sweep = def.Trash.Sweep
	}
}

func saveConfig(cfg Config) error {
	f, err := os.Create(DefaultConfigFile)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := yaml.NewEncoder(f)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		return err
	}

	return nil
}
