package model

import (
	"time"

	"feedvideo.local/shared-events"
)

// LikeKey is the canonical natural key of a durable like relation.
type LikeKey struct {
	ActorID    string
	TargetID   string
	TargetType string
}

// LikePendingEvent persists notification intent in the same write as its state.
type LikePendingEvent struct {
	EventID   string       `bson:"event_id" json:"event_id"`
	Event     events.Event `bson:"event" json:"event"`
	SizeBytes int64        `bson:"size_bytes" json:"size_bytes"`
}

// LikeState is the final relation state. ID is a stable natural-key hash;
// inactive relations remain durable so their versions cannot move backwards.
type LikeState struct {
	ID                string             `bson:"_id" json:"id"`
	ActorID           string             `bson:"actor_id" json:"actor_id"`
	TargetID          string             `bson:"target_id" json:"target_id"`
	TargetType        string             `bson:"target_type" json:"target_type"`
	Active            bool               `bson:"active" json:"active"`
	Version           int64              `bson:"version" json:"version"`
	UpdatedAt         time.Time          `bson:"updated_at" json:"updated_at"`
	PendingProjection bool               `bson:"pending_projection" json:"-"`
	PendingEvents     []LikePendingEvent `bson:"pending_events" json:"-"`
	PendingEventBytes int64              `bson:"pending_event_bytes" json:"-"`
}
