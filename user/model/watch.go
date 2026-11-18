package model

import (
	events "feedvideo.local/shared-events"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"time"
)

// WatchRecord is a durable v1 fact. Legacy UserBehaviorLog retains its own metric.
type WatchRecord struct {
	events.WatchEvent `bson:",inline"`
	ActorKind         string    `bson:"actor_kind"`
	ActorID           string    `bson:"actor_id"`
	ReceivedAt        time.Time `bson:"received_at"`
	DurationMS        int64     `bson:"duration_ms"`
	PayloadHash       string    `bson:"payload_hash"`
}

// Preserve exact occurrence precision alongside the queryable BSON DateTime.
func (r WatchRecord) MarshalBSON() ([]byte, error) {
	type fields WatchRecord
	return bson.Marshal(struct {
		Record         fields `bson:",inline"`
		OccurredAtNano string `bson:"occurred_at_nano"`
	}{fields(r), r.OccurredAt.UTC().Format(time.RFC3339Nano)})
}
func (r *WatchRecord) UnmarshalBSON(raw []byte) error {
	type fields WatchRecord
	var stored struct {
		Record         fields `bson:",inline"`
		OccurredAtNano string `bson:"occurred_at_nano"`
	}
	if err := bson.Unmarshal(raw, &stored); err != nil {
		return err
	}
	*r = WatchRecord(stored.Record)
	if stored.OccurredAtNano != "" {
		exact, err := time.Parse(time.RFC3339Nano, stored.OccurredAtNano)
		if err != nil || !exact.Truncate(time.Millisecond).Equal(r.OccurredAt) {
			return fmt.Errorf("invalid exact watch occurrence")
		}
		r.OccurredAt = exact
	}
	return nil
}

type WatchResult struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	Code    string `json:"code"`
}
