package repo

import (
	"context"
	"errors"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type WatchRepo struct{ col *mongo.Collection }

func NewWatchRepo(db *mongo.Database) *WatchRepo {
	if db == nil {
		return &WatchRepo{}
	}
	return &WatchRepo{col: db.Collection("behavior_events_v1", options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary()))}
}
func (r *WatchRepo) EnsureIndexes(ctx context.Context) error {
	if r == nil || r.col == nil {
		return errors.New("watch storage unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "actor_id", Value: 1}, {Key: "event_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("watch_actor_event")},
		{Keys: bson.D{{Key: "actor_id", Value: 1}, {Key: "payload.playback_session_id", Value: 1}, {Key: "payload.seq", Value: 1}}, Options: options.Index().SetUnique(true).SetName("watch_actor_session_seq")},
		{Keys: bson.D{{Key: "received_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(90 * 24 * 60 * 60).SetName("watch_receipt_ttl")},
		{Keys: bson.D{{Key: "actor_id", Value: 1}, {Key: "occurred_at", Value: -1}, {Key: "event_id", Value: -1}}, Options: options.Index().SetName("watch_actor_history")},
		{Keys: bson.D{{Key: "actor_id", Value: 1}, {Key: "payload.playback_session_id", Value: 1}, {Key: "payload.video_id", Value: 1}, {Key: "payload.content_version", Value: 1}, {Key: "occurred_at", Value: -1}, {Key: "event_id", Value: -1}}, Options: options.Index().SetName("watch_session_history")},
	})
	return err
}
func validWatchRecord(record *model.WatchRecord) bool {
	return record != nil && record.ActorKind == "user" && events.WatchActorValid(record.ActorID) && !record.ReceivedAt.IsZero() && record.DurationMS > 0 && record.DurationMS <= 86400000 && record.Validate(record.ReceivedAt, record.DurationMS) == "" && record.PayloadHash == events.WatchPayloadHash(record.WatchEvent)
}

// Insert confirms an immutable fact. A duplicate index is not sufficient for
// duplicate success: both natural keys and the canonical payload must agree.
func (r *WatchRepo) Insert(ctx context.Context, record *model.WatchRecord) (string, error) {
	if !validWatchRecord(record) {
		return "", errors.New("invalid watch record")
	}
	if r == nil || r.col == nil {
		return "", errors.New("watch storage unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_, err := r.col.InsertOne(ctx, record)
	if err == nil {
		return "accepted", nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return "", err
	}
	filter := bson.M{"actor_id": record.ActorID, "$or": bson.A{bson.M{"event_id": record.EventID}, bson.M{"payload.playback_session_id": record.Payload.PlaybackSessionID, "payload.seq": record.Payload.Seq}}}
	cursor, err := r.col.Find(ctx, filter, options.Find().SetLimit(2).SetBatchSize(2))
	if err != nil {
		return "", err
	}
	defer cursor.Close(ctx)
	var originals []*model.WatchRecord
	if err = cursor.All(ctx, &originals); err != nil {
		return "", err
	}
	if len(originals) == 0 {
		return "", errors.New("duplicate fact unavailable")
	}
	for _, old := range originals {
		if !validWatchRecord(old) || old.ActorID != record.ActorID {
			return "", errors.New("invalid stored watch fact")
		}
		if old.PayloadHash != record.PayloadHash || old.Payload.PlaybackSessionID != record.Payload.PlaybackSessionID || old.Payload.Seq != record.Payload.Seq {
			return "CONFLICT", nil
		}
	}
	return "duplicate", nil
}
