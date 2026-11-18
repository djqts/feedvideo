package repo

import (
	"context"
	"fmt"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PublicVideoLister reads bounded public pages directly from the current source.
// Private and administrative callers retain the generic VideoRepo.List contract.
type PublicVideoLister interface {
	ListPublicVideos(context.Context, int64, int64) ([]*model.Video, int64, error)
}

func (r *VideoRepoMongo) ListPublicVideos(ctx context.Context, page, pageSize int64) ([]*model.Video, int64, error) {
	if page < 1 || page > 100 || pageSize < 1 || pageSize > 100 {
		return nil, 0, ErrVideoReadInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, 0, ErrVideoReadUnavailable
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := dbCtx.Deadline()
	filter := PublicVideoFilter()
	total, err := r.mdb.Col().CountDocuments(dbCtx, filter, options.Count().SetMaxTime(time.Until(deadline)))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if err := dbCtx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if total < 0 {
		return nil, 0, ErrVideoReadUnavailable
	}
	opts := options.Find().SetSkip((page - 1) * pageSize).SetLimit(pageSize).
		SetBatchSize(int32(pageSize)).SetMaxTime(time.Until(deadline)).
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}})
	cur, err := r.mdb.Col().Find(dbCtx, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	defer cur.Close(dbCtx)
	videos := make([]*model.Video, 0, pageSize)
	seen := make(map[primitive.ObjectID]struct{}, pageSize)
	var rawCount int64
	for cur.Next(dbCtx) {
		rawCount++
		if rawCount > pageSize {
			return nil, 0, ErrVideoReadUnavailable
		}
		video, err := decodeCurrentVideo(cur.Current)
		if err != nil {
			return nil, 0, err
		}
		if video == nil || video.ID.IsZero() {
			return nil, 0, ErrVideoReadUnavailable
		}
		if _, duplicate := seen[video.ID]; duplicate {
			return nil, 0, ErrVideoReadUnavailable
		}
		seen[video.ID] = struct{}{}
		// An explicit tombstone, including BSON null, is never public. Typed
		// decoding alone cannot distinguish a missing deleted_at from null.
		if cur.Current.Lookup("deleted_at").Type == 0 && model.IsPubliclyVisible(video) {
			videos = append(videos, video)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if err := dbCtx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	return videos, total, nil
}
