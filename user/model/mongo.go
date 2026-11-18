package model

import (
	"time"
)

type MongoUser struct {
    UserID    uint `bson:"_id" json:"user_id"`
    UserName  string `bson:"username" json:"username"`
	Avatar string `bson:"avatar" json:"avatar"`//头像图片路径
	Bio string `bson:"bio" json:"bio"`//个人简介
	Gender string `bson:"gender" json:"gender"`
	Birthday time.Time `bson:"birthday" json:"birthday"`
	City string `bson:"city,omitempty" json:"city"`
	// 动态标签+权重（推荐系统用）
	Interests []InterestTag `bson:"interests,omitempty" json:"interests"`
	Verified  bool          `bson:"verified" json:"verified"`
	// 行为统计
	BehaviorStats BehaviorStats `bson:"behavior_stats,omitempty" json:"behavior_stats"`
	// ...其他字段
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// 动态标签+权重（推荐系统用）
type InterestTag struct {
    Tag string `bson:"tag" json:"tag"`
    Weight float64 `bson:"weight" json:"weight"`
	LastInterest int64 `bson:"last_interest" json:"last_interest"`
}
// 行为统计
type BehaviorStats struct {
    AvgSessionDuration int `bson:"avg_session_duration" json:"avg_session_duration"`
	TopActiveHours []int `bson:"top_active_hours" json:"top_active_hours"`
	VideoCompletionRate float64 `bson:"video_completion_rate" json:"video_completion_rate"`
}

// 用户行为埋点日志
type UserBehaviorLog struct {
	ID         string    `bson:"_id,omitempty" json:"id"`
	UserID     int64     `bson:"user_id" json:"user_id"`
	Action     string    `bson:"action" json:"action"`           // play|like|collect|share|follow
	TargetID   string    `bson:"target_id" json:"target_id"`     // int64→string (video ObjectID / user ID)
	TargetType string    `bson:"target_type" json:"target_type"` // "video"|"user"
	Timestamp  time.Time `bson:"timestamp" json:"timestamp"`
	Duration   int       `bson:"duration" json:"duration"`       // 预留,当前填0; 后续前C上报播放时长
}
// 用户通知设置（灵活 schema 用 MongoDB）
type UserNotificationSetting struct {
	UserID       int64 `bson:"user_id" json:"user_id"`
	LikePush     bool  `bson:"like_push" json:"like_push"`
	CommentPush  bool  `bson:"comment_push" json:"comment_push"`
	FollowPush   bool  `bson:"follow_push" json:"follow_push"`
}