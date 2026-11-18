package model

import (
	"time"
)

// UserCounter 用户相关计数缓存（高频读写字段）
type UserCounter struct {
	UserID         int64 `json:"user_id"`
	FollowerCount  int64 `json:"follower_count"`
	FollowingCount int64 `json:"following_count"`
	PostCount      int64 `json:"post_count"`
	LikeCount      int64 `json:"like_count"`
}
// UserOnlineStatus 在线状态缓存
type UserOnlineStatus struct {
	UserID   int64     `json:"user_id"`
	IsOnline bool      `json:"is_online"`
	LastSeen time.Time `json:"last_seen"`
}
// InterestCache 推荐标签快照（可定期计算后缓存）
type InterestCache struct {
	UserID  int64     `json:"user_id"`
	TopTags []string  `json:"top_tags"`
	Weights []float64 `json:"weights"`
}
