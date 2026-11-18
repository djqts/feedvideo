package model

// VideoLikeSnapshot is one receipt from a confirmed legacy relation hash.
type VideoLikeSnapshot struct {
	VideoID string
	Epoch   string
	Version int64
	Count   int64
	Active  bool
	Changed bool
}
