package model

import "time"

// UserRelationVersion retains one monotonic version per unordered user pair,
// including after relation deletion so a later follow cannot reuse an event ID.
type UserRelationVersion struct {
	LowID     int64     `gorm:"column:low_id;primaryKey;autoIncrement:false"`
	HighID    int64     `gorm:"column:high_id;primaryKey;autoIncrement:false"`
	Version   int64     `gorm:"not null;default:0"`
	UpdatedAt time.Time `gorm:"type:datetime(3);not null"`
}

func (UserRelationVersion) TableName() string { return "user_relation_versions" }
