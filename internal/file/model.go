package file

// Type 字段取值。
const (
	TypeFile int8 = 1 // 普通文件
	TypeDir  int8 = 2 // 目录
)

// Status 字段取值。
const (
	StatusNormal  int8 = 1 // 正常
	StatusTrash   int8 = 2 // 回收站
	StatusDeleted int8 = 3 // 已删除
)

type File struct {
	ID         uint64 `gorm:"primaryKey" json:"id"`
	OwnerID    uint64 `gorm:"index;not null" json:"owner_id"`
	ParentID   uint64 `gorm:"index;not null;default:0" json:"parent_id"` // 0 表示根目录
	Name       string `gorm:"size:255;not null" json:"name"`
	Type       int8   `gorm:"not null;default:1" json:"type"`   // 1文件 2目录
	Size       int64  `gorm:"not null;default:0" json:"size"`   // 当前版本大小
	Hash       string `gorm:"size:64;index" json:"hash"`        // 当前版本文件 hash
	StorageKey string `gorm:"size:512" json:"storage_key"`      // 当前版本对象 key
	Status     int8   `gorm:"not null;default:1" json:"status"` // 1正常 2回收站 3已删除
}
