package model

import videomodel "feedvideo.local/video/model"

// FeedType 流类型（常量 + handler 大小写容错）
type FeedType string

const (
	FeedTypeRecommend FeedType = "recommend"
	FeedTypeFollowing FeedType = "following"
	FeedTypeNearby    FeedType = "nearby"
	FeedTypeHot       FeedType = "hot"
)

// FeedItem 单条 Feed（视频 + 统计）
type FeedItem struct {
	Video          *videomodel.Video      `json:"video"`
	Stats          *videomodel.VideoStats `json:"stats"`
	LikeStateKnown bool                   `json:"like_state_known"`
	IsLiked        bool                   `json:"is_liked"`
	IsCollected    bool                   `json:"is_collected"`
	CollectCount   int64                  `json:"collect_count"`
	ShareCount     int64                  `json:"share_count"`
}

// FeedReq Feed 请求参数
type FeedReq struct {
	Type   FeedType
	UserID string
	Size   int64
	Lng    float64
	Lat    float64
	Radius float64
}

// FeedResp Feed 响应
type FeedResp struct {
	List    []*FeedItem `json:"list"`
	HasMore bool        `json:"has_more"`
}
