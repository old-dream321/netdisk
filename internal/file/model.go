package file

import (
	"time"
)

const (
	TypeFile int8 = 1 // 普通文件
	TypeDir  int8 = 2 // 目录
)

const (
	StatusNormal int8 = 1 // 正常
	StatusTrash  int8 = 2 // 回收站
)

type File struct {
	ID         uint64     `gorm:"primaryKey" json:"id"`
	OwnerID    uint64     `gorm:"index;not null" json:"owner_id"`
	ParentID   uint64     `gorm:"index;not null;default:0" json:"parent_id"` // 0 表示根目录
	Name       string     `gorm:"size:255;not null" json:"name"`
	Type       int8       `gorm:"not null;default:1" json:"type"`   // 1文件 2目录
	Size       int64      `gorm:"not null;default:0" json:"size"`   // 大小
	Hash       string     `gorm:"size:64;index" json:"hash"`        // 文件 hash
	StorageKey string     `gorm:"size:512" json:"storage_key"`      // 对象 key
	Status     int8       `gorm:"not null;default:1" json:"status"` // 1正常 2回收站
	DeletedAt  *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}
