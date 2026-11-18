package events

// Behavior action constants
const (
	ActionPlay    = "play"
	ActionLike    = "like"
	ActionCollect = "collect"
	ActionShare   = "share"
	ActionFollow  = "follow"
)

// Behavior target type constants
const (
	TargetTypeVideo = "video"
	TargetTypeUser  = "user"
)

// AllActions returns all tracked action types for aggregation initialization.
func AllActions() []string {
	return []string{ActionPlay, ActionLike, ActionCollect, ActionShare, ActionFollow}
}
