package model

import "time"

// UploadObject records a planned target and its verified object metadata.
// ObjectVersion is empty for a bucket without object versioning.
type UploadObject struct {
	Kind          string `bson:"kind" json:"kind"`
	Bucket        string `bson:"bucket" json:"bucket"`
	ObjectKey     string `bson:"object_key" json:"object_key"`
	Size          int64  `bson:"size" json:"size"`
	ContentType   string `bson:"content_type" json:"content_type"`
	SHA256        string `bson:"sha256" json:"sha256"`
	ObjectVersion string `bson:"object_version" json:"object_version"`
}

// Upload retains object targets even when an upload or publish result is unknown.
type Upload struct {
	ID               string         `bson:"_id" json:"upload_id"`
	OwnerID          string         `bson:"owner_id" json:"owner_id"`
	Status           string         `bson:"status" json:"status"`
	Objects          []UploadObject `bson:"objects" json:"objects"`
	ContentVersion   int64          `bson:"content_version" json:"content_version"`
	Version          int64          `bson:"version" json:"version"`
	LeaseExpiresAt   time.Time      `bson:"lease_expires_at" json:"lease_expires_at"`
	MaximumExpiresAt time.Time      `bson:"maximum_expires_at" json:"maximum_expires_at"`
	CreatedAt        time.Time      `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time      `bson:"updated_at" json:"updated_at"`
	PublishKey       string         `bson:"publish_key,omitempty" json:"-"`
	PayloadHash      string         `bson:"payload_hash,omitempty" json:"-"`
	VideoID          string         `bson:"video_id,omitempty" json:"video_id,omitempty"`
	JobID            string         `bson:"job_id,omitempty" json:"job_id,omitempty"`
}

// PublishReceipt is the immutable first accepted result, independent of later
// processing, edits and deletion of the video.
type PublishReceipt struct {
	VideoID        string `bson:"video_id" json:"video_id"`
	MediaStatus    string `bson:"media_status" json:"media_status"`
	JobID          string `bson:"job_id" json:"job_id"`
	ContentVersion int64  `bson:"content_version" json:"content_version"`
}
