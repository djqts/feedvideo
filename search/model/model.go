package model

import (
	videomodel "feedvideo.local/video/model"
)

// SearchType 搜索类型
type SearchType string

const (
	SearchTypeVideo SearchType = "video"
	SearchTypeUser  SearchType = "user"
	SearchTypeTag   SearchType = "tag"
	SearchTypeAll   SearchType = "all"
)

// SearchReq 搜索请求参数
type SearchReq struct {
	Q               string     `json:"q"`
	Type            SearchType `json:"type"`
	Page            int64      `json:"page"`
	Size            int64      `json:"size"`
	CandidateCursor string     `json:"candidate_cursor,omitempty"`
}

// HotWord 热词
type HotWord struct {
	Word  string  `json:"word"`
	Score float64 `json:"score"`
}

// SuggestItem 联想词
type SuggestItem struct {
	Word  string  `json:"word"`
	Score float64 `json:"score"`
}

// TagResult tag 搜索标签聚合
type TagResult struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

const (
	SearchSchemaVersion       = 3
	CategoryStatusOK          = "ok"
	CategoryStatusUnavailable = "unavailable"
)

// SearchCategories contains only the categories requested by the caller.
type SearchCategories struct {
	Video *VideoCategory `json:"video,omitempty"`
	User  *UserCategory  `json:"user,omitempty"`
	Tag   *TagCategory   `json:"tag,omitempty"`
}

type VideoCategory struct {
	Status        string              `json:"status"`
	Items         []*videomodel.Video `json:"items"`
	Total         int64               `json:"total"`
	HasMore       bool                `json:"has_more"`
	TotalRelation string              `json:"total_relation"`
}

type UserCategory struct {
	Status  string        `json:"status"`
	Items   []interface{} `json:"items"`
	Total   int64         `json:"total"`
	HasMore bool          `json:"has_more"`
}

type TagCategory struct {
	Status  string       `json:"status"`
	Items   []*TagResult `json:"items"`
	Total   int64        `json:"total"`
	HasMore bool         `json:"has_more"`
}

// SearchResp retains the legacy fields alongside explicit category availability.
type SearchResp struct {
	SchemaVersion    int                 `json:"schema_version"`
	Partial          bool                `json:"partial"`
	Categories       *SearchCategories   `json:"categories"`
	Total            int64               `json:"total"`
	Videos           []*videomodel.Video `json:"videos"`
	Users            []interface{}       `json:"users"`
	Tags             []*TagResult        `json:"tags"`
	TotalRelation    string              `json:"total_relation"`
	NextCursor       string              `json:"next_cursor,omitempty"`
	HasMore          bool                `json:"has_more,omitempty"`
	ScanLimitReached bool                `json:"scan_limit_reached,omitempty"`
}
