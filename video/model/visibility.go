package model

// IsPubliclyVisible requires moderation approval and a playable media asset.
// Empty media status is retained for approved videos published before the
// media-processing state machine existed.
func IsPubliclyVisible(video *Video) bool {
	return video != nil && video.DeletedAt == nil && video.Status == "approved" &&
		(video.MediaStatus == "" || video.MediaStatus == "succeeded")
}
