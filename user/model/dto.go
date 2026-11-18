package model

import "time"

// UserRegisterRequest 注册请求
type UserRegisterRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

// UserLoginRequest 登录请求
type UserLoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

//relation status
const (
	RelationStatusNone   int8 = 0
	RelationStatusFollow int8 = 1
	RelationStatusBlack  int8 = 2
)

// UserProfileResponse 用户资料响应（组合多源数据）
type UserProfileResponse struct {
	UserID         int64    `json:"user_id"`
	Username       string   `json:"username"`
	Avatar         string   `json:"avatar"`
	Bio            string   `json:"bio"`
	Gender         string   `json:"gender"`
	City           string   `json:"city"`
	FollowerCount  int64    `json:"follower_count"`
	FollowingCount int64    `json:"following_count"`
	PostCount      int64    `json:"post_count"`
	IsDegraded     bool     `json:"is_degraded"`
	IsOnline       bool     `json:"is_online"`
	LastSeen       int64    `json:"last_seen"`
	TopTags        []string `json:"top_tags"` // 推荐标签展示
}

// UserProfileRequest 用户资料请求
type UserProfileRequest struct {
	Username string    `json:"username"`
	Avatar   *string   `json:"avatar"`
	Bio      *string   `json:"bio"`
	Gender   string    `json:"gender"`
	City     string    `json:"city"`
	UpdateAt time.Time `json:"update_at"`
}

// SearchRequest 搜索请求
type SearchRequest struct {
	Query string `json:"query" form:"query"`
	Page  int    `json:"page" form:"page"`
	Size  int    `json:"size" form:"size"`
	Type  string `json:"type" form:"type"` // user / post / tag
}

// SearchResponse 搜索响应
type SearchResponse struct {
	Total int         `json:"total"`
	Items interface{} `json:"items"`
}

type UserRename struct {
	NewUsername string `json:"new_username" binding:"required"`
}
