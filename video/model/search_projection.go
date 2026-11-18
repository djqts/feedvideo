package model

import "time"

// SearchProjection is the durable intent stored atomically with its source video.
type SearchProjection struct {
	SourceVersion  int64     `bson:"source_version"`
	AppliedVersion int64     `bson:"applied_version"`
	LeaseToken     int64     `bson:"lease_token"`
	Dirty          bool      `bson:"dirty"`
	NextAttemptAt  time.Time `bson:"next_attempt_at"`
	LeaseUntil     time.Time `bson:"lease_until"`
	DirtySince     time.Time `bson:"dirty_since"`
	Attempts       int64     `bson:"attempts"`
	LeaseOwner     string    `bson:"lease_owner"`
	LastErrorCode  string    `bson:"last_error_code"`
}

type SearchProjectionLease struct {
	Video         *Video
	SourceVersion int64
	Token         int64
	Attempt       int64
	Owner         string
	Until         time.Time
}
