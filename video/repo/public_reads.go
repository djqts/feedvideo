package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// CurrentVideoReader reads current source facts without consulting caches.
type CurrentVideoReader interface {
	GetCurrentByID(context.Context, string) (*model.Video, error)
	BatchGetCurrentVideos(context.Context, []string) (map[string]*model.Video, error)
}

var (
	ErrVideoReadUnavailable = errors.New("video source is temporarily unavailable")
	ErrVideoReadInvalid     = errors.New("invalid video source read")
	ErrVideoNotFound        = errors.New("video not found")
)

// PublicVideoFilter returns a fresh copy of the shared Mongo public predicate.
// Public search can add author selection without changing private List callers.
func PublicVideoFilter() bson.M { return publicHotVideoFilter() }

func (r *VideoRepoMongo) GetCurrentByID(ctx context.Context, id string) (*model.Video, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadInvalid, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, ErrVideoReadUnavailable
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	col, err := r.mdb.Col().Clone(options.Collection().SetReadPreference(readpref.Primary()))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	filter := currentLiveVideoFilter()
	filter["_id"] = oid
	deadline, _ := dbCtx.Deadline()
	raw, err := col.FindOne(dbCtx, filter, options.FindOne().SetMaxTime(time.Until(deadline))).Raw()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("%w: %w", ErrVideoNotFound, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	video, err := decodeCurrentVideo(raw)
	if err != nil {
		return nil, err
	}
	if err := dbCtx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if video == nil || video.ID != oid {
		return nil, ErrVideoReadUnavailable
	}
	return video, nil
}

func (r *VideoRepoMongo) BatchGetCurrentVideos(ctx context.Context, ids []string) (map[string]*model.Video, error) {
	if len(ids) > 200 {
		return nil, ErrVideoReadInvalid
	}
	oids := make([]primitive.ObjectID, 0, len(ids))
	wanted := make(map[primitive.ObjectID]struct{}, len(ids))
	for _, id := range ids {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrVideoReadInvalid, err)
		}
		if _, exists := wanted[oid]; !exists {
			wanted[oid] = struct{}{}
			oids = append(oids, oid)
		}
	}
	out := make(map[string]*model.Video, len(oids))
	if len(oids) == 0 {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, ErrVideoReadUnavailable
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filter := currentLiveVideoFilter()
	filter["_id"] = bson.M{"$in": oids}
	deadline, _ := dbCtx.Deadline()
	opts := options.Find().SetLimit(int64(len(oids))).SetBatchSize(int32(len(oids))).SetMaxTime(time.Until(deadline))
	cur, err := r.mdb.Col().Find(dbCtx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	defer cur.Close(dbCtx)
	for cur.Next(dbCtx) {
		video, err := decodeCurrentVideo(cur.Current)
		if err != nil {
			return nil, err
		}
		if _, exists := wanted[video.ID]; !exists {
			return nil, ErrVideoReadUnavailable
		}
		key := video.ID.Hex()
		if _, duplicate := out[key]; duplicate {
			return nil, ErrVideoReadUnavailable
		}
		out[key] = video
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if err := dbCtx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	return out, nil
}

// BSON's string decoder also accepts null, symbols and binary values. Inspect
// the visibility fields before typed decoding can turn them into allowed text.
func decodeCurrentVideo(raw bson.Raw) (*model.Video, error) {
	elements, err := raw.Elements()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	for _, element := range elements {
		field := element.Key()
		if (field == "status" || field == "media_status") && element.Value().Type != bson.TypeString {
			return nil, fmt.Errorf("%w: invalid %s BSON type", ErrVideoReadUnavailable, field)
		}
	}
	var video model.Video
	if err := bson.Unmarshal(raw, &video); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	return &video, nil
}

// Live source reads deliberately retain pending/failed videos for current
// author ownership checks. An explicit deletion field, including null, is dead.
func currentLiveVideoFilter() bson.M {
	return bson.M{
		"status":     bson.M{"$ne": "deleted"},
		"deleted_at": bson.M{"$exists": false},
		"$expr":      bson.M{"$eq": bson.A{bson.M{"$type": "$deleted_at"}, "missing"}},
	}
}
