package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Geo 地理位置，MongoDB GeoJSON 格式
type Geo struct {
	Type        string    `bson:"type" json:"type"`               // "Point"
	Coordinates []float64 `bson:"coordinates" json:"coordinates"` // [longitude, latitude]
}

// Video 视频完整信息
type Video struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Version          int64              `bson:"version" json:"version,string"`
	SearchProjection *SearchProjection  `bson:"search_projection,omitempty" json:"-"`
	Title            string             `bson:"title" json:"title"`
	Description      string             `bson:"description" json:"description"`
	Tags             []string           `bson:"tags" json:"tags"`                     // 多值索引
	AuthorID         string             `bson:"author_id,omitempty" json:"author_id"` // 发布者 = JWT sub
	Location         *Geo               `bson:"location,omitempty" json:"location"`
	Music            string             `bson:"music,omitempty" json:"music"`
	CoverURL         string             `bson:"cover_url" json:"cover_url"`
	VideoURL         string             `bson:"video_url" json:"video_url"` // 对象存储 URL
	Status           string             `bson:"status" json:"status"`       // pending, approved, rejected, deleted
	CreatedAt        time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time          `bson:"updated_at" json:"updated_at"`
	DeletedAt        *time.Time         `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`

	// 媒体处理元数据（由 media-worker 写入）
	Duration         float64 `bson:"duration,omitempty" json:"duration,omitempty"`                       // 秒
	Width            int     `bson:"width,omitempty" json:"width,omitempty"`                             // 像素
	Height           int     `bson:"height,omitempty" json:"height,omitempty"`                           // 像素
	TranscodeStatus  string  `bson:"transcode_status,omitempty" json:"transcode_status,omitempty"`       // pending/processing/ready/failed
	TranscodedURL    string  `bson:"transcoded_url,omitempty" json:"transcoded_url,omitempty"`           // 480p 转码对象 URL
	TranscodedURL720 string  `bson:"transcoded_url_720p,omitempty" json:"transcoded_url_720p,omitempty"` // 720p 转码对象 URL

	// 媒体处理状态机（P3.1）：uploaded → validated → queued → processing → succeeded/failed
	// 兼容保留 TranscodeStatus，新状态统一走 MediaStatus。
	MediaStatus string `bson:"media_status,omitempty" json:"media_status,omitempty"`
	// MediaError 记录最近一次处理失败的机器可读原因（不包含对象存储凭据）。
	MediaError string `bson:"media_error,omitempty" json:"media_error,omitempty"`
	// RetryCount 当前任务已重试次数（worker 维护）。
	RetryCount     int             `bson:"retry_count,omitempty" json:"retry_count,omitempty"`
	ContentVersion int64           `bson:"content_version,omitempty" json:"content_version,omitempty"`
	MediaJob       *MediaJob       `bson:"media_job,omitempty" json:"-"`
	UploadID       string          `bson:"upload_id,omitempty" json:"upload_id,omitempty"`
	PublishKey     string          `bson:"publish_key,omitempty" json:"-"`
	PublishHash    string          `bson:"publish_hash,omitempty" json:"-"`
	PublishReceipt *PublishReceipt `bson:"publish_receipt,omitempty" json:"-"`
}

// VideoStats 暂存容器，可直接映射 Redis Hash 字段
type VideoStats struct {
	VideoID      string `json:"video_id"`
	PlayCount    int64  `json:"play_count"`
	LikeCount    int64  `json:"like_count"`
	CommentCount int64  `json:"comment_count"`
	CollectCount int64  `json:"collect_count"`
	ShareCount   int64  `json:"share_count"`
}

// Comment 评论，嵌套回复
type Comment struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	VideoID           string             `bson:"video_id" json:"video_id"`
	UserID            string             `bson:"user_id" json:"user_id"`
	Content           string             `bson:"content" json:"content"`
	ParentID          string             `bson:"parent_id,omitempty" json:"parent_id,omitempty"` // 回复的父评论ID
	Replies           []Comment          `bson:"replies,omitempty" json:"replies,omitempty"`     // 内嵌回复，避免递归查询
	LikeCount         int64              `bson:"like_count" json:"like_count"`
	Status            string             `bson:"status" json:"status"` // active, reported, hidden
	CreatedAt         time.Time          `bson:"created_at" json:"created_at"`
	LegacyLikeEpoch   string             `bson:"legacy_like_epoch,omitempty" json:"-"`
	LegacyLikeVersion int64              `bson:"legacy_like_version" json:"-"`
}

// LikeRecord 点赞持久化记录，异步从 Redis 双写到 MongoDB
type LikeRecord struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID     string             `bson:"user_id" json:"user_id"`
	TargetID   string             `bson:"target_id" json:"target_id"`     // 视频ID 或 评论ID
	TargetType string             `bson:"target_type" json:"target_type"` // "video" 或 "comment"
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}

// ESSearchDoc 存储在 Elasticsearch 中的视频搜索文档
type ESSearchDoc struct {
	VideoID     string    `json:"video_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}
