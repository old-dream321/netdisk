package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func NewSQLiteDB(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		// 让 GORM 把驱动的原生错误翻译成统一的语义化错误，
		// 例如 SQLite 的 "UNIQUE constraint failed" → gorm.ErrDuplicatedKey。
		// 这样业务代码可以用 errors.Is 判断，而不用去匹配错误字符串。
		TranslateError: true,
	})
	if err != nil {
		return nil, err
	}

	// SQLite 同时只允许一个写事务，限制连接数为 1 可以避免 "database is locked"。
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	return db, nil
}

// Migrate 按传入的模型建表，缺少的表会新建，已有的表只补齐新增字段。
func Migrate(db *gorm.DB, models ...any) error {
	return db.AutoMigrate(models...)
}
