package model

import "gorm.io/gorm"

type User struct {
	gorm.Model
	Username     string       `gorm:"unique;not null" json:"username"`
	PasswordHash string       `gorm:"not null" json:"-"`
	Status       int          `gorm:"default:1" json:"status"` 
}
type UserFollow struct {
	gorm.Model
	FollowerID  int64     `gorm:"uniqueIndex:idx_follower_following;column:follower_id;index"` 
	FollowingID int64     `gorm:"uniqueIndex:idx_follower_following;column:following_id;index"` 
	Status  int8          `gorm:"column:status;default:1" json:"status"`
}