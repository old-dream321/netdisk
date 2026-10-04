package share

import (
	"time"
)

type Share struct {
	ID        uint64     `gorm:"primaryKey" json:"id"`
	Token     string     `gorm:"size:64;uniqueIndex;not null" json:"token"`
	OwnerID   uint64     `gorm:"index; not null" json:"owner_id"`
	FileID    uint64     `gorm:"index; not null" json:"file_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}
