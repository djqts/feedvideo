package model

// 用户索引
type UserDocument struct {
	UserID        int64    `json:"user_id"`
	Username      string   `json:"username"`
	Tags          []string `json:"tags"`           // 搜索时的分词字段
	FollowerCount int64    `json:"follower_count"` // 可用于排序
}

// 标签维度搜索索引（用于标签热度、聚合搜索）
type TagDocument struct {
	TagName       string `json:"tag_name"`
	PostCount     int64  `json:"post_count"`
	FollowerCount int64  `json:"follower_count"`
}