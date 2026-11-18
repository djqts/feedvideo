package model

// CommentLikeSnapshot is an internal receipt from one confirmed Redis hash.
type CommentLikeSnapshot struct {
	CommentID string
	Epoch     string
	Version   int64
	Count     int64
	Active    bool
	Changed   bool
}
