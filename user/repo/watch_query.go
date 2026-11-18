package repo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrWatchQueryInvalid = errors.New("invalid watch query")
var ErrWatchSessionTooLarge = errors.New("watch session exceeds bounded aggregation")

type WatchHistoryPage struct {
	Records    []*model.WatchRecord
	HasMore    bool
	NextCursor string
}
type watchCursorData struct {
	Version int    `json:"v"`
	Actor   string `json:"a"`
	Time    int64  `json:"t"`
	EventID string `json:"e"`
}

func (r *WatchRepo) History(ctx context.Context, actor string, limit int, cursor string) (*WatchHistoryPage, error) {
	if !events.WatchActorValid(actor) || limit < 1 || limit > 200 {
		return nil, ErrWatchQueryInvalid
	}
	filter := bson.M{"actor_id": actor}
	if cursor != "" {
		if len(cursor) > 512 {
			return nil, ErrWatchQueryInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || !events.WatchJSONUnambiguous(raw) {
			return nil, ErrWatchQueryInvalid
		}
		var data watchCursorData
		probeTime := time.Now().UTC()
		probe := events.WatchEvent{SchemaVersion: 1, Type: "behavior.pause", OccurredAt: probeTime, Payload: events.WatchPayload{VideoID: "507f1f77bcf86cd799439011", ContentVersion: 1, PlaybackSessionID: "4e2f9e73-ec81-4bf6-a448-0ac14b2a9911", PlaybackRate: 1}}
		decodeErr := json.Unmarshal(raw, &data)
		probe.EventID = data.EventID
		if decodeErr != nil || data.Version != 1 || data.Actor != actor || data.Time <= 0 || data.Time > time.Now().Add(5*time.Minute).UnixMilli() || probe.Validate(probeTime, 0) != "" {
			return nil, ErrWatchQueryInvalid
		}
		filter["$or"] = bson.A{bson.M{"occurred_at": bson.M{"$lt": time.UnixMilli(data.Time)}}, bson.M{"occurred_at": time.UnixMilli(data.Time), "event_id": bson.M{"$lt": data.EventID}}}
	}
	records, err := r.readWatch(ctx, filter, int64(limit+1))
	if err != nil {
		return nil, err
	}
	page := &WatchHistoryPage{Records: records, HasMore: len(records) > limit}
	if page.HasMore {
		page.Records = records[:limit]
		last := page.Records[limit-1]
		raw, _ := json.Marshal(watchCursorData{1, actor, last.OccurredAt.UnixMilli(), last.EventID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
func (r *WatchRepo) Session(ctx context.Context, actor, session, video string, version int64) ([]*model.WatchRecord, error) {
	// Reuse the pure envelope identity validator without media/time side effects.
	probe := events.WatchEvent{SchemaVersion: 1, EventID: session, Type: "behavior.pause", OccurredAt: time.Now().UTC(), Payload: events.WatchPayload{PlaybackSessionID: session, VideoID: video, ContentVersion: version, PlaybackRate: 1}}
	if !events.WatchActorValid(actor) || probe.Validate(probe.OccurredAt, 0) != "" {
		return nil, ErrWatchQueryInvalid
	}
	rows, err := r.readWatch(ctx, bson.M{"actor_id": actor, "payload.playback_session_id": session, "payload.video_id": video, "payload.content_version": version}, 5001)
	if err != nil {
		return nil, err
	}
	if len(rows) > 5000 {
		return nil, ErrWatchSessionTooLarge
	}
	return rows, nil
}
func (r *WatchRepo) readWatch(ctx context.Context, filter bson.M, limit int64) ([]*model.WatchRecord, error) {
	if r == nil || r.col == nil {
		return nil, errors.New("watch storage unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	cursor, err := r.col.Find(ctx, filter, options.Find().SetLimit(limit).SetBatchSize(int32(limit)).SetSort(bson.D{{Key: "occurred_at", Value: -1}, {Key: "event_id", Value: -1}}).SetMaxTime(time.Until(deadline)))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	rows := make([]*model.WatchRecord, 0)
	if err = cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for _, record := range rows {
		if !validWatchRecord(record) || record.ActorID != filter["actor_id"] {
			return nil, errors.New("invalid watch source")
		}
		if session, ok := filter["payload.playback_session_id"]; ok && (record.Payload.PlaybackSessionID != session || record.Payload.VideoID != filter["payload.video_id"] || record.Payload.ContentVersion != filter["payload.content_version"]) {
			return nil, errors.New("invalid watch session source")
		}
	}
	return rows, nil
}
