package model

import (
	"encoding/json"
	"time"

	videomodel "feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Topic 话题文档
type Topic struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name        string             `bson:"name" json:"name"`
	Description string             `bson:"description,omitempty" json:"description,omitempty"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}

// MarshalJSON preserves stored IDs while giving metadata-free tags a null ID.
func (t Topic) MarshalJSON() ([]byte, error) {
	type topicJSON Topic
	var id *string
	if t.ID != primitive.NilObjectID {
		value := t.ID.Hex()
		id = &value
	}
	return json.Marshal(struct {
		topicJSON
		ID        *string `json:"id"`
		Persisted bool    `json:"persisted"`
	}{topicJSON: topicJSON(t), ID: id, Persisted: id != nil})
}

// TopicDetail 话题详情（含视频列表）
type TopicDetail struct {
	Topic       *Topic              `json:"topic"`
	Videos      []*videomodel.Video `json:"videos"`
	TotalVideos int64               `json:"total_videos"`
	Page        int64               `json:"page"`
	HasMore     bool                `json:"has_more"`
}
