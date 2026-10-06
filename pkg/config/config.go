package config

import (
	"crypto/rand"
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
	Auth     AuthConfig     `yaml:"auth"`
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

type AuthConfig struct {
	// Secret 是 HMAC 签名密钥， 换掉它会让所有人下线
	Secret string        `yaml:"secret"`
	TTL    time.Duration `yaml:"ttl"`
}

type TrashConfig struct {
	// 回收站里的保留时长
	TTL time.Duration `yaml:"ttl"`
	// 扫描间隔。
	Sweep time.Duration `yaml:"sweep"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

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
		Auth:     AuthConfig{TTL: 24 * time.Hour}, // Secret 留空：Load 里生成
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
		cfg.normalize() // 生成 auth.secret
		if err := saveConfig(cfg); err != nil {
			log.Printf("无法生成默认配置文件（将只使用内置默认值）: %v", err)
		} else {
			log.Printf("已生成默认配置文件 %s（含随机 auth.secret），修改后重启即可生效", DefaultConfigFile)
		}
		return cfg, nil
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件 %s: %w", DefaultConfigFile, err)
	}
	if cfg.normalize() {
		// 把新生成的密钥写回去
		if err := saveConfig(cfg); err != nil {
			return Config{}, fmt.Errorf("把生成的 auth.secret 写回 %s: %w", DefaultConfigFile, err)
		}
		log.Printf("已在 %s 里生成随机 auth.secret；换掉它会让所有人下线", DefaultConfigFile)
	}
	return cfg, nil
}

// 补默认值。返回值表示需要写回文件
func (c *Config) normalize() bool {
	def := Default()
	changed := false

	if c.Server.Addr == "" {
		c.Server.Addr = def.Server.Addr
	}
	if c.Database.DSN == "" {
		c.Database.DSN = def.Database.DSN
	}
	if c.Storage.Dir == "" {
		c.Storage.Dir = def.Storage.Dir
	}

	if c.Auth.Secret == "" {
		c.Auth.Secret = newSecret()
		changed = true
	}
	if c.Auth.TTL <= 0 {
		log.Printf("auth.ttl 未设置或不是正数，使用默认值 %s", def.Auth.TTL)
		c.Auth.TTL = def.Auth.TTL
	}

	if c.Trash.TTL <= 0 {
		log.Printf("trash.ttl 未设置或不是正数，使用默认值 %s", def.Trash.TTL)
		c.Trash.TTL = def.Trash.TTL
	}
	if c.Trash.Sweep <= 0 {
		log.Printf("trash.sweep 未设置或不是正数，使用默认值 %s", def.Trash.Sweep)
		c.Trash.Sweep = def.Trash.Sweep
	}

	return changed
}

func newSecret() string {
	return rand.Text()
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
