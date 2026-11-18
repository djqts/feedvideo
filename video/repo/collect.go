package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"feedvideo.local/shared-db/cache"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// CollectRepo retains the legacy toggle and read API.
type CollectRepo interface {
	Collect(ctx context.Context, userID, videoID string) (collected bool, collectCount int64, err error)
	IsCollected(ctx context.Context, userID, videoID string) (bool, error)
	GetCollectedVideos(ctx context.Context, userID string, page, size int64) ([]string, int64, error)
	GetCollectCount(ctx context.Context, videoID string) (int64, error)
	BatchCollectCounts(ctx context.Context, videoIDs []string) (map[string]int64, error)
	BatchIsCollected(ctx context.Context, userID string, videoIDs []string) (map[string]bool, error)
}

type CollectStateStore interface {
	SetCollectState(context.Context, string, string, bool, *int64) (*CollectState, error)
	GetCollectState(context.Context, string, string) (*CollectState, error)
	EnsureIndexes(context.Context) error
}

type CollectState struct {
	Collected bool
	Version   int64
	Changed   bool // Internal deduplication only; never persisted.
}

var (
	ErrCollectInvalid     = errors.New("invalid collect state")
	ErrCollectUnavailable = errors.New("collect state unavailable")
)

type CollectVersionConflict struct{ Current *CollectState }

func (e *CollectVersionConflict) Error() string { return "collect version conflict" }

// CollectRecord keeps absent legacy fields distinct from modern false/zero.
type CollectRecord struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	UserID    string             `bson:"user_id"`
	VideoID   string             `bson:"video_id"`
	CreatedAt time.Time          `bson:"created_at"`
	Active    *bool              `bson:"active,omitempty"`
	Version   *int64             `bson:"version,omitempty"`
	UpdatedAt time.Time          `bson:"updated_at,omitempty"`
}

type CollectRepoMongo struct {
	col *mongo.Collection
}

var _ CollectRepo = (*CollectRepoMongo)(nil)
var _ CollectStateStore = (*CollectRepoMongo)(nil)

// Construction performs no network calls. The Redis parameter is retained for
// callers; collection state and reads always use Mongo. Startup ensures indexes.
func NewCollectRepoMongo(db *mongo.Database, rdb *cache.RedisClient) *CollectRepoMongo {
	r := &CollectRepoMongo{}
	if db != nil {
		r.col = db.Collection("collects")
	}
	return r
}

func collectUnavailable(cause error) error { return fmt.Errorf("%w: %w", ErrCollectUnavailable, cause) }

func validateCollectUser(userID string) error {
	n, err := strconv.ParseInt(userID, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != userID {
		return ErrCollectInvalid
	}
	return nil
}
func validateCollectVideo(videoID string) error {
	id, err := primitive.ObjectIDFromHex(videoID)
	if err != nil || id.Hex() != videoID {
		return ErrCollectInvalid
	}
	return nil
}
func validateCollectKey(userID, videoID string) error {
	if err := validateCollectUser(userID); err != nil {
		return err
	}
	return validateCollectVideo(videoID)
}
func validateCollectBatch(videoIDs []string) error {
	if len(videoIDs) > 100 {
		return ErrCollectInvalid
	}
	for _, id := range videoIDs {
		if err := validateCollectVideo(id); err != nil {
			return err
		}
	}
	return nil
}
func (r *CollectRepoMongo) ready() error {
	if r == nil || r.col == nil {
		return collectUnavailable(errors.New("Mongo collect store not configured"))
	}
	return nil
}

// Raw state decoding prevents partial or null fields from acquiring legacy defaults.
func (r *CollectRepoMongo) read(ctx context.Context, userID, videoID string) (*CollectState, primitive.ObjectID, bool, bool, error) {
	if err := r.ready(); err != nil {
		return nil, primitive.NilObjectID, false, false, err
	}
	raw, err := r.col.FindOne(ctx, bson.M{"user_id": userID, "video_id": videoID}).DecodeBytes()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return &CollectState{}, primitive.NilObjectID, false, false, nil
	}
	if err != nil {
		return nil, primitive.NilObjectID, false, false, collectUnavailable(err)
	}
	id, ok := raw.Lookup("_id").ObjectIDOK()
	if !ok {
		return nil, primitive.NilObjectID, false, false, collectUnavailable(errors.New("malformed collect identity"))
	}
	a, v := raw.Lookup("active"), raw.Lookup("version")
	if a.Type == 0 && v.Type == 0 {
		return &CollectState{Collected: true, Version: 1}, id, true, true, nil
	}
	active, validActive := a.BooleanOK()
	if !validActive || (v.Type != bson.TypeInt32 && v.Type != bson.TypeInt64) || v.AsInt64() < 1 {
		return nil, primitive.NilObjectID, false, false, collectUnavailable(errors.New("malformed collect state"))
	}
	return &CollectState{Collected: active, Version: v.AsInt64()}, id, true, false, nil
}

func (r *CollectRepoMongo) GetCollectState(ctx context.Context, userID, videoID string) (*CollectState, error) {
	if err := validateCollectKey(userID, videoID); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, _, _, _, err := r.read(ctx, userID, videoID)
	return state, err
}
func (r *CollectRepoMongo) SetCollectState(ctx context.Context, userID, videoID string, active bool, expected *int64) (*CollectState, error) {
	if err := validateCollectKey(userID, videoID); err != nil {
		return nil, err
	}
	if expected != nil && *expected < 0 {
		return nil, ErrCollectInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.set(ctx, userID, videoID, active, expected)
}

// set retries a fixed target only after known CAS losses or duplicate keys.
// Unknown write outcomes return immediately; a PUT can safely read back.
func (r *CollectRepoMongo) set(ctx context.Context, userID, videoID string, active bool, expected *int64) (*CollectState, error) {
	for attempt := 0; attempt < 8; attempt++ {
		current, id, exists, legacy, err := r.read(ctx, userID, videoID)
		if err != nil {
			return nil, err
		}
		if expected != nil && *expected != current.Version {
			return nil, &CollectVersionConflict{Current: current}
		}
		if current.Collected == active {
			return current, nil
		}
		if current.Version == math.MaxInt64 {
			return nil, ErrCollectInvalid
		}
		next := &CollectState{Collected: active, Version: current.Version + 1, Changed: true}
		now := time.Now().UTC()
		if !exists {
			record := &CollectRecord{ID: primitive.NewObjectID(), UserID: userID, VideoID: videoID, CreatedAt: now, Active: &active, Version: &next.Version, UpdatedAt: now}
			_, err := r.col.InsertOne(ctx, record)
			if err == nil {
				return next, nil
			}
			if mongo.IsDuplicateKeyError(err) {
				continue
			}
			return nil, collectUnavailable(err)
		}
		filter := bson.M{"_id": id, "version": current.Version, "active": current.Collected}
		if legacy {
			filter["active"] = bson.M{"$exists": false}
			filter["version"] = bson.M{"$exists": false}
		}
		update := bson.M{"$set": bson.M{"active": active, "version": next.Version, "updated_at": now}}
		result, err := r.col.UpdateOne(ctx, filter, update)
		if err != nil {
			return nil, collectUnavailable(err)
		}
		if result.MatchedCount != 0 {
			return next, nil
		}
	}
	return nil, collectUnavailable(errors.New("collect contention retry budget exhausted"))
}

// Collect remains non-idempotent across requests. A concurrent edit conflicts
// instead of retrying into another toggle internally.
func (r *CollectRepoMongo) Collect(ctx context.Context, userID, videoID string) (bool, int64, error) {
	if err := validateCollectKey(userID, videoID); err != nil {
		return false, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, _, _, _, err := r.read(ctx, userID, videoID)
	if err != nil {
		return false, 0, err
	}
	state, err := r.set(ctx, userID, videoID, !current.Collected, &current.Version)
	if err != nil {
		return false, 0, err
	}
	n, err := r.count(ctx, bson.M{"video_id": videoID})
	if err != nil {
		return state.Collected, 0, fmt.Errorf("collect state may already be committed; exact count failed: %w", err)
	}
	return state.Collected, n, nil
}
func (r *CollectRepoMongo) IsCollected(ctx context.Context, userID, videoID string) (bool, error) {
	state, err := r.GetCollectState(ctx, userID, videoID)
	if err != nil {
		return false, err
	}
	return state.Collected, nil
}

// Whole BSON type checks exclude arrays as well as partial modern records.
func collectActiveFilter(base bson.M) bson.M {
	base["$or"] = bson.A{
		bson.M{"active": true, "version": bson.M{"$gte": int64(1)}, "$expr": bson.M{"$and": bson.A{
			bson.M{"$eq": bson.A{bson.M{"$type": "$active"}, "bool"}},
			bson.M{"$in": bson.A{bson.M{"$type": "$version"}, bson.A{"int", "long"}}},
		}}},
		bson.M{"active": bson.M{"$exists": false}, "version": bson.M{"$exists": false}},
	}
	return base
}
func (r *CollectRepoMongo) count(ctx context.Context, base bson.M) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	n, err := r.col.CountDocuments(ctx, collectActiveFilter(base))
	if err != nil {
		return 0, collectUnavailable(err)
	}
	return n, nil
}
func (r *CollectRepoMongo) GetCollectCount(ctx context.Context, videoID string) (int64, error) {
	if err := validateCollectVideo(videoID); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.count(ctx, bson.M{"video_id": videoID})
}

func (r *CollectRepoMongo) GetCollectedVideos(ctx context.Context, userID string, page, size int64) ([]string, int64, error) {
	if err := validateCollectUser(userID); err != nil {
		return nil, 0, err
	}
	if page < 1 || size < 1 || size > 100 || page > math.MaxInt64/size {
		return nil, 0, ErrCollectInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	total, err := r.count(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, 0, err
	}
	if total == 0 || (page-1)*size >= total {
		return []string{}, total, nil
	}
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: collectActiveFilter(bson.M{"user_id": userID})}},
		bson.D{{Key: "$set", Value: bson.M{"collect_order_at": bson.M{"$ifNull": bson.A{"$updated_at", "$created_at"}}}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "collect_order_at", Value: -1}, {Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$skip", Value: (page - 1) * size}},
		bson.D{{Key: "$limit", Value: size}},
		bson.D{{Key: "$project", Value: bson.M{"video_id": 1, "_id": 0}}},
	}
	cur, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, collectUnavailable(err)
	}
	defer cur.Close(ctx)
	var rows []struct {
		VideoID string `bson:"video_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, 0, collectUnavailable(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if validateCollectVideo(row.VideoID) != nil {
			return nil, 0, collectUnavailable(errors.New("malformed collect list identity"))
		}
		ids = append(ids, row.VideoID)
	}
	return ids, total, nil
}

func (r *CollectRepoMongo) BatchCollectCounts(ctx context.Context, videoIDs []string) (map[string]int64, error) {
	if err := validateCollectBatch(videoIDs); err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(videoIDs))
	for _, id := range videoIDs {
		result[id] = 0
	}
	if len(videoIDs) == 0 {
		return result, nil
	}
	if err := r.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: collectActiveFilter(bson.M{"video_id": bson.M{"$in": videoIDs}})}},
		bson.D{{Key: "$group", Value: bson.M{"_id": "$video_id", "count": bson.M{"$sum": 1}}}},
	}
	cur, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, collectUnavailable(err)
	}
	defer cur.Close(ctx)
	var rows []struct {
		VideoID string `bson:"_id"`
		Count   int64  `bson:"count"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, collectUnavailable(err)
	}
	for _, row := range rows {
		if _, ok := result[row.VideoID]; !ok || row.Count < 0 {
			return nil, collectUnavailable(errors.New("malformed collect count result"))
		}
		result[row.VideoID] = row.Count
	}
	return result, nil
}
func (r *CollectRepoMongo) BatchIsCollected(ctx context.Context, userID string, videoIDs []string) (map[string]bool, error) {
	if err := validateCollectUser(userID); err != nil {
		return nil, err
	}
	if err := validateCollectBatch(videoIDs); err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(videoIDs))
	for _, id := range videoIDs {
		result[id] = false
	}
	if len(videoIDs) == 0 {
		return result, nil
	}
	if err := r.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: collectActiveFilter(bson.M{"user_id": userID, "video_id": bson.M{"$in": videoIDs}})}},
		bson.D{{Key: "$group", Value: bson.M{"_id": "$video_id"}}},
	}
	cur, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, collectUnavailable(err)
	}
	defer cur.Close(ctx)
	var rows []struct {
		VideoID string `bson:"_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, collectUnavailable(err)
	}
	for _, row := range rows {
		if _, ok := result[row.VideoID]; !ok {
			return nil, collectUnavailable(errors.New("malformed collect membership result"))
		}
		result[row.VideoID] = true
	}
	return result, nil
}

func (r *CollectRepoMongo) EnsureIndexes(ctx context.Context) error {
	if err := r.ready(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		// Preserve the existing driver's default name of the unique index.
		{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "video_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "active", Value: 1}, {Key: "version", Value: 1}, {Key: "updated_at", Value: -1}, {Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "video_id", Value: 1}, {Key: "active", Value: 1}, {Key: "version", Value: 1}, {Key: "user_id", Value: 1}}},
	})
	if err != nil {
		return collectUnavailable(err)
	}
	return nil
}
