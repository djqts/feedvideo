package repo

import (
	"context"
	"fmt"
	"math"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ListByAuthors reads bounded public candidates in a stable creation order.
// The caller must still recheck current facts before publishing these bodies.
func (r *VideoRepoMongo) ListByAuthors(ctx context.Context, authorIDs []string, page, size int64) ([]*model.Video, error) {
	if len(authorIDs) > 500 || page < 1 || page > 100 || size < 1 || size > 50 {
		return nil, ErrVideoReadInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if len(authorIDs) == 0 {
		return []*model.Video{}, nil
	}
	authors := make(map[string]struct{}, len(authorIDs))
	for _, id := range authorIDs {
		authors[id] = struct{}{}
	}
	filter := PublicVideoFilter()
	filter["author_id"] = bson.M{"$in": authorIDs}
	opts := options.Find().SetSkip((page - 1) * size).
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}})
	return r.findFeedCandidates(ctx, filter, opts, size, authors)
}

// GeoSearch retains Mongo's $near distance order and requires a public asset.
func (r *VideoRepoMongo) GeoSearch(ctx context.Context, lng, lat, radiusMeters float64, limit int64) ([]*model.Video, error) {
	if !finiteCoordinate(lng) || !finiteCoordinate(lat) || !finiteCoordinate(radiusMeters) ||
		lng < -180 || lng > 180 || lat < -90 || lat > 90 || radiusMeters < 1 || radiusMeters > 100000 || limit < 1 || limit > 50 {
		return nil, ErrVideoReadInvalid
	}
	filter := PublicVideoFilter()
	filter["location"] = bson.M{"$near": bson.M{
		"$geometry":    bson.M{"type": "Point", "coordinates": []float64{lng, lat}},
		"$maxDistance": radiusMeters,
	}}
	return r.findFeedCandidates(ctx, filter, options.Find(), limit, nil)
}

func finiteCoordinate(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func (r *VideoRepoMongo) findFeedCandidates(ctx context.Context, filter bson.M, opts *options.FindOptions, size int64, authors map[string]struct{}) ([]*model.Video, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, ErrVideoReadUnavailable
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := dbCtx.Deadline()
	opts.SetLimit(size).SetBatchSize(int32(size)).SetMaxTime(time.Until(deadline))
	cur, err := r.mdb.Col().Find(dbCtx, filter, opts)
	if err != nil {
		return nil, feedCandidateReadError(dbCtx, err)
	}
	defer cur.Close(dbCtx)
	results := make([]*model.Video, 0, size)
	seen := make(map[primitive.ObjectID]struct{}, size)
	var rawCount int64
	for cur.Next(dbCtx) {
		rawCount++
		if rawCount > size {
			return nil, ErrVideoReadUnavailable
		}
		video, err := decodeFeedCandidate(cur.Current, authors)
		if err != nil {
			return nil, feedCandidateReadError(dbCtx, err)
		}
		if _, duplicate := seen[video.ID]; duplicate {
			return nil, ErrVideoReadUnavailable
		}
		seen[video.ID] = struct{}{}
		if cur.Current.Lookup("deleted_at").Type == 0 && model.IsPubliclyVisible(video) {
			results = append(results, video)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, feedCandidateReadError(dbCtx, err)
	}
	if err := dbCtx.Err(); err != nil {
		return nil, feedCandidateReadError(dbCtx, err)
	}
	return results, nil
}

func feedCandidateReadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
}

func decodeFeedCandidate(raw bson.Raw, authors map[string]struct{}) (*model.Video, error) {
	elements, err := raw.Elements()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	var id primitive.ObjectID
	var author string
	idFields, authorFields := 0, 0
	for _, element := range elements {
		switch element.Key() {
		case "_id":
			idFields++
			value := element.Value()
			if value.Type != bson.TypeObjectID {
				return nil, ErrVideoReadUnavailable
			}
			id = value.ObjectID()
		case "author_id":
			if authors == nil {
				continue
			}
			authorFields++
			value := element.Value()
			if value.Type != bson.TypeString {
				return nil, ErrVideoReadUnavailable
			}
			author = value.StringValue()
		}
	}
	if idFields != 1 || id.IsZero() {
		return nil, ErrVideoReadUnavailable
	}
	if authors != nil {
		if _, requested := authors[author]; authorFields != 1 || !requested {
			return nil, ErrVideoReadUnavailable
		}
	}
	video, err := decodeCurrentVideo(raw)
	if err != nil {
		return nil, err
	}
	if video == nil || video.ID != id || authors != nil && video.AuthorID != author {
		return nil, ErrVideoReadUnavailable
	}
	return video, nil
}
