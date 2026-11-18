package model

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var ErrReplayConflict = errors.New("notification event replay content conflict")
var ErrInvalidEvent = errors.New("invalid notification event")

// Notification 通知文档，MongoDB 集合 notifications，每通知一文档。
type Notification struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	EventID        string             `bson:"event_id,omitempty" json:"event_id,omitempty"` // 幂等键
	PayloadHash    string             `bson:"payload_hash,omitempty" json:"-"`
	EventCreatedAt int64              `bson:"event_created_at,omitempty" json:"-"`
	RecipientID    string             `bson:"recipient_id" json:"recipient_id"`
	Type           string             `bson:"type" json:"type"`
	ActorID        string             `bson:"actor_id,omitempty" json:"actor_id,omitempty"`
	TargetID       string             `bson:"target_id,omitempty" json:"target_id,omitempty"`
	TargetType     string             `bson:"target_type,omitempty" json:"target_type,omitempty"`
	Content        string             `bson:"content,omitempty" json:"content,omitempty"`
	Read           bool               `bson:"read" json:"read"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
}
