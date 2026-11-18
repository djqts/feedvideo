package model

// ReportItem ES reports 索引的举报项
type ReportItem struct {
	ID           string `json:"id"`
	CommentID    string `json:"comment_id"`
	Reason       string `json:"reason"`
	CreatedAt    string `json:"created_at"`
	Content      string `json:"content,omitempty"`
	ReporterName string `json:"reporter_name,omitempty"`
	Status       string `json:"status"`
	// ReportedUserID is the author of the reported comment, so the console can
	// offer ban/mute actions without a second lookup.
	ReportedUserID string `json:"reported_user_id,omitempty"`
}
