package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"netdisk/internal/file"
	"netdisk/internal/router"
	"netdisk/internal/user"
	"netdisk/pkg/config"
	"netdisk/pkg/database"
	"netdisk/pkg/session"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	dbpath := cfg.Database.DSN
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

	sessions, err := session.NewFileStore(cfg.Session.File, cfg.Session.TTL)
	if err != nil {
		log.Fatalf("初始化会话存储失败: %v", err)
	}

	e := router.New(router.Deps{DB: db, Sessions: sessions, StorageDir: cfg.Storage.Dir})

	// 后台回收站清理
	/*
		Go的ctx负责让后台任务在主进程退出（或请求取消）时能结束，而不是继续进行
		这里使用 context.WithCancel 来创建一个可取消的ctx，并在主进程退出时调用 cancel() 来通知清理结束。
		这里的ctx是自己维护的，而请求的ctx是由echo框架传入的，二者不一样。请求的ctx只在请求处理期间有效，后台任务需要一个独立的ctx。
		关于echo文档里的优雅关闭，那里监听了系统信号 SIGINT/SIGTERM，但是这里不需要，因为echo的Start()方法本身会阻塞并监听系统信号，直到服务退出，所以在Start()之后的代码就是服务退出后的清理工作。
	*/
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		file.StartJanitor(ctx, db, cfg.Storage.Dir, cfg.Trash.TTL, cfg.Trash.Sweep)
	}()

	log.Printf("netdisk 启动，监听 %s（存储目录 %s，会话 TTL %s，回收站保留 %s）",
		cfg.Server.Addr, cfg.Storage.Dir, cfg.Session.TTL, cfg.Trash.TTL)
	if err := e.Start(cfg.Server.Addr); err != nil {
		log.Fatalf("服务退出: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		log.Printf("后台清理收尾超时，强制退出")
	}
}

// shutdownGrace 是等后台任务收尾的上限。
const shutdownGrace = 5 * time.Second
