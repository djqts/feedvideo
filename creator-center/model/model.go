package model

// StatsResp 创作者数据聚合响应
type StatsResp struct {
	Scope         string `json:"scope"`
	PlayBasis     string `json:"play_basis"`
	FollowerCount int64  `json:"follower_count"`
	TotalPlays    int64  `json:"total_plays"`
	TotalLikes    int64  `json:"total_likes"`
	TotalComments int64  `json:"total_comments"`
	TotalShares   int64  `json:"total_shares"`
	TotalCollects int64  `json:"total_collects"`
	VideoCount    int64  `json:"video_count"`
}

// RevenueResp 预估收益响应
type RevenueResp struct {
	EstimatedRevenue float64 `json:"estimated_revenue"`
	TotalPlays       int64   `json:"total_plays"`
	CPM              float64 `json:"cpm"`
}

// SummaryResp keeps one aggregation and its estimate in separate objects.
type SummaryResp struct {
	Stats   *StatsResp   `json:"stats"`
	Revenue *RevenueResp `json:"revenue"`
}
