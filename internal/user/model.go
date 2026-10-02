package user

type User struct {
	ID       uint   `gorm:"primaryKey autoIncrement" json:"id"`
	Username string `gorm:"unique;not null" json:"username"`
	Password string `gorm:"not null" json:"password"`
	Email    string `gorm:"unique;not null" json:"email"`
	Quota    int64  `gorm:"not null default:0" json:"quota"`
	Used     int64  `gorm:"not null default:0" json:"used"`
}
