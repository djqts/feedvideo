package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"feedvideo.local/shared-events"
	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	ErrLikeInvalid              = errors.New("invalid like state")
	ErrLikeBackpressure         = errors.New("like notification backlog full")
	ErrLikeUnavailable          = errors.New("like state unavailable")
	ErrLikeDurableWriteDisabled = errors.New("durable likes require the state service")
	ErrLikeLegacySyncDisabled   = errors.New("legacy like synchronization disabled in durable mode")
)

const (
	likePendingLimit           = 128
	likePendingByteLimit int64 = 256 * 1024
	likePageLimit              = 100
	likeCASAttempts            = 8
)

type LikeVersionConflict struct{ Current *model.LikeState }

func (e *LikeVersionConflict) Error() string { return "like version conflict" }

type LikeStateStore interface {
	SetLikeState(context.Context, model.LikeKey, bool, *int64, *events.Event) (*model.LikeState, error)
	GetLikeState(context.Context, model.LikeKey) (*model.LikeState, error)
	CountActive(context.Context, string, string) (int64, error)
	ListLikeStates(context.Context, string, int, bool) ([]*model.LikeState, error)
	ClearLikeProjection(context.Context, string, int64) (bool, error)
	AckLikeEvent(context.Context, string, string) error
	EnsureIndexes(context.Context) error
}

type likeStateRepo struct{ col *mongo.Collection }

func NewLikeStateRepo(db *mongo.Database) LikeStateStore {
	r := &likeStateRepo{}
	if db != nil {
		r.col = db.Collection("like_states")
	}
	return r
}

func likeUnavailable(cause error) error { return fmt.Errorf("%w: %w", ErrLikeUnavailable, cause) }
func validateLikeTarget(targetID, targetType string) error {
	if targetType != events.TargetVideo && targetType != events.TargetComment {
		return ErrLikeInvalid
	}
	id, err := primitive.ObjectIDFromHex(targetID)
	if err != nil || id.Hex() != targetID {
		return ErrLikeInvalid
	}
	return nil
}
func validateLikeKey(key model.LikeKey) error {
	n, err := strconv.ParseUint(key.ActorID, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != key.ActorID {
		return ErrLikeInvalid
	}
	return validateLikeTarget(key.TargetID, key.TargetType)
}
func validLikeRelationID(id string) bool {
	if len(id) != 32 && len(id) != 64 {
		return false
	}
	b, err := hex.DecodeString(id)
	return err == nil && hex.EncodeToString(b) == id
}
func likeDefault(key model.LikeKey) *model.LikeState {
	sum := sha256.Sum256([]byte(key.ActorID + "\x00" + key.TargetType + "\x00" + key.TargetID))
	return &model.LikeState{ID: hex.EncodeToString(sum[:]), ActorID: key.ActorID, TargetID: key.TargetID, TargetType: key.TargetType, PendingEvents: []model.LikePendingEvent{}}
}
func (r *likeStateRepo) read(ctx context.Context, key model.LikeKey) (*model.LikeState, bool, error) {
	if r == nil || r.col == nil {
		return nil, false, likeUnavailable(errors.New("Mongo like store not configured"))
	}
	var state model.LikeState
	err := r.col.FindOne(ctx, bson.M{"actor_id": key.ActorID, "target_type": key.TargetType, "target_id": key.TargetID}).Decode(&state)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return likeDefault(key), false, nil
	}
	if err != nil {
		return nil, false, likeUnavailable(err)
	}
	return &state, true, nil
}

// SetLikeState commits state and notification intent in one document, within
// one total five-second budget and a bounded number of CAS rereads.
func (r *likeStateRepo) SetLikeState(ctx context.Context, key model.LikeKey, active bool, expected *int64, intent *events.Event) (*model.LikeState, error) {
	if err := validateLikeKey(key); err != nil {
		return nil, err
	}
	if expected != nil && *expected < 0 {
		return nil, ErrLikeInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < likeCASAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, likeUnavailable(err)
		}
		current, exists, err := r.read(ctx, key)
		if err != nil {
			return nil, err
		}
		if expected != nil && *expected != current.Version {
			return nil, &LikeVersionConflict{Current: current}
		}
		if current.Active == active {
			return current, nil
		}
		if current.Version < 0 || current.Version == math.MaxInt64 || current.PendingEventBytes < 0 {
			return nil, ErrLikeInvalid
		}
		if len(current.PendingEvents) >= likePendingLimit || current.PendingEventBytes >= likePendingByteLimit {
			return nil, ErrLikeBackpressure
		}
		next := *current
		next.PendingEvents = append([]model.LikePendingEvent{}, current.PendingEvents...)
		next.Active, next.Version, next.UpdatedAt, next.PendingProjection = active, current.Version+1, time.Now().UTC(), true
		var pending *model.LikePendingEvent
		if active && intent != nil {
			if intent.Type != events.NotifLike || intent.ActorID != key.ActorID || intent.TargetID != key.TargetID || intent.TargetType != key.TargetType {
				return nil, ErrLikeInvalid
			}
			event := *intent
			event.EventID = "like:" + current.ID + ":" + strconv.FormatInt(next.Version, 10)
			if !validLikeRelationID(current.ID) || len(event.EventID) > 128 {
				return nil, ErrLikeInvalid
			}
			if event.CreatedAt == 0 {
				event.CreatedAt = next.UpdatedAt.Unix()
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrLikeInvalid, err)
			}
			pending = &model.LikePendingEvent{EventID: event.EventID, Event: event, SizeBytes: int64(len(encoded))}
			if pending.SizeBytes > likePendingByteLimit-current.PendingEventBytes {
				return nil, ErrLikeBackpressure
			}
			next.PendingEvents = append(next.PendingEvents, *pending)
			next.PendingEventBytes += pending.SizeBytes
		}
		if !exists {
			_, err := r.col.InsertOne(ctx, &next)
			if err == nil {
				return &next, nil
			}
			if mongo.IsDuplicateKeyError(err) {
				continue
			}
			return nil, likeUnavailable(err)
		}
		// Capacity fences use the document at commit time. A relay acknowledgment
		// can free capacity concurrently without having its array overwritten.
		maxBytes := likePendingByteLimit - 1
		if pending != nil {
			maxBytes = likePendingByteLimit - pending.SizeBytes
		}
		filter := bson.M{"_id": current.ID, "version": current.Version, "active": current.Active,
			"pending_event_bytes": bson.M{"$lte": maxBytes},
			"$expr":               bson.M{"$lt": bson.A{bson.M{"$size": bson.M{"$ifNull": bson.A{"$pending_events", bson.A{}}}}, likePendingLimit}},
		}
		update := bson.M{"$set": bson.M{"active": active, "version": next.Version, "updated_at": next.UpdatedAt, "pending_projection": true}}
		if pending != nil {
			update["$push"] = bson.M{"pending_events": pending}
			update["$inc"] = bson.M{"pending_event_bytes": pending.SizeBytes}
		}
		result, err := r.col.UpdateOne(ctx, filter, update)
		if err != nil {
			return nil, likeUnavailable(err)
		}
		if result.MatchedCount != 0 {
			return &next, nil
		}
	}
	return nil, likeUnavailable(errors.New("like state contention retry budget exhausted"))
}

func (r *likeStateRepo) GetLikeState(ctx context.Context, key model.LikeKey) (*model.LikeState, error) {
	if err := validateLikeKey(key); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, _, err := r.read(ctx, key)
	return state, err
}

func (r *likeStateRepo) CountActive(ctx context.Context, targetID, targetType string) (int64, error) {
	if err := validateLikeTarget(targetID, targetType); err != nil {
		return 0, err
	}
	if r == nil || r.col == nil {
		return 0, likeUnavailable(errors.New("Mongo like store not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := r.col.CountDocuments(ctx, bson.M{"target_id": targetID, "target_type": targetType, "active": true})
	if err != nil {
		return 0, likeUnavailable(err)
	}
	return n, nil
}

func (r *likeStateRepo) ListLikeStates(ctx context.Context, afterID string, limit int, pendingOnly bool) ([]*model.LikeState, error) {
	if afterID != "" && !validLikeRelationID(afterID) {
		return nil, ErrLikeInvalid
	}
	if r == nil || r.col == nil {
		return nil, likeUnavailable(errors.New("Mongo like store not configured"))
	}
	if limit <= 0 || limit > likePageLimit {
		limit = likePageLimit
	}
	filter := bson.M{}
	if afterID != "" {
		filter["_id"] = bson.M{"$gt": afterID}
	}
	if pendingOnly {
		filter["$or"] = bson.A{bson.M{"pending_projection": true}, bson.M{"pending_events.0": bson.M{"$exists": true}}}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cur, err := r.col.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, likeUnavailable(err)
	}
	defer cur.Close(ctx)
	var rows []*model.LikeState
	if err := cur.All(ctx, &rows); err != nil {
		return nil, likeUnavailable(err)
	}
	return rows, nil
}

func (r *likeStateRepo) ClearLikeProjection(ctx context.Context, id string, version int64) (bool, error) {
	if !validLikeRelationID(id) || version < 0 {
		return false, ErrLikeInvalid
	}
	if r == nil || r.col == nil {
		return false, likeUnavailable(errors.New("Mongo like store not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := r.col.UpdateOne(ctx, bson.M{"_id": id, "version": version, "pending_projection": true}, bson.M{"$set": bson.M{"pending_projection": false}})
	if err != nil {
		return false, likeUnavailable(err)
	}
	return res.MatchedCount != 0, nil
}

// AckLikeEvent filters the current array and recomputes its remaining budget
// atomically. Duplicate acknowledgments cannot subtract bytes twice.
func (r *likeStateRepo) AckLikeEvent(ctx context.Context, id, eventID string) error {
	if !validLikeRelationID(id) || eventID == "" || len(eventID) > 128 {
		return ErrLikeInvalid
	}
	if r == nil || r.col == nil {
		return likeUnavailable(errors.New("Mongo like store not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$set", Value: bson.M{"pending_events": bson.M{"$filter": bson.M{"input": bson.M{"$ifNull": bson.A{"$pending_events", bson.A{}}}, "as": "event", "cond": bson.M{"$ne": bson.A{"$$event.event_id", bson.M{"$literal": eventID}}}}}}}},
		bson.D{{Key: "$set", Value: bson.M{"pending_event_bytes": bson.M{"$sum": "$pending_events.size_bytes"}}}},
	}
	_, err := r.col.UpdateOne(ctx, bson.M{"_id": id, "pending_events.event_id": eventID}, pipeline)
	if err != nil {
		return likeUnavailable(err)
	}
	return nil
}

func (r *likeStateRepo) EnsureIndexes(ctx context.Context) error {
	if r == nil || r.col == nil {
		return likeUnavailable(errors.New("Mongo like store not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "actor_id", Value: 1}, {Key: "target_type", Value: 1}, {Key: "target_id", Value: 1}}, Options: options.Index().SetName("like_natural_key").SetUnique(true)},
		{Keys: bson.D{{Key: "target_type", Value: 1}, {Key: "target_id", Value: 1}, {Key: "active", Value: 1}}, Options: options.Index().SetName("like_target_active")},
		{Keys: bson.D{{Key: "pending_projection", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("like_pending_projection")},
		{Keys: bson.D{{Key: "pending_events.0", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("like_pending_events")},
	})
	if err != nil {
		return likeUnavailable(err)
	}
	return nil
}
