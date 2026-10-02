package main

import (
	"log"
	"os"
	"path/filepath"

	"netdisk/internal/file"
	"netdisk/internal/router"
	"netdisk/internal/user"
	"netdisk/pkg/config"
	"netdisk/pkg/database"
)

func main() {
	cfg := config.Load()

	dbpath := cfg.DatabaseDSN
	if err := os.MkdirAll(dbpath, 0o755); err != nil {
		log.Fatalf("创建数据库目录失败: %v", err)
	}

	db, err := database.NewSQLiteDB(filepath.Join(dbpath, "netdisk.db"))
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	if err := database.Migrate(db, &user.User{}, &file.File{}); err != nil {
		log.Fatalf("初始化数据表失败: %v", err)
	}

	e := router.New(router.Deps{DB: db})

	log.Printf("netdisk 启动，监听 %s", cfg.Addr)
	if err := e.Start(cfg.Addr); err != nil {
		log.Fatalf("服务退出: %v", err)
	}
}
