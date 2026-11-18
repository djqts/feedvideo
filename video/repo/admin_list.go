package repo

import (
	"context"
	"fmt"
	"time"

	"feedvideo.local/video/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ListVideosByStatus returns non-deleted videos in an exact moderation state.
// It backs the admin review console, so it is intentionally separate from the
// public list that only exposes approved and media-ready documents.
func (r *VideoRepoMongo) ListVideosByStatus(ctx context.Context, status string, page, pageSize int64) ([]*model.Video, int64, error) {
	switch status {
	case "pending", "rejected", "approved":
	default:
		return nil, 0, ErrVideoReadInvalid
	}
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
	filter := bson.M{
		"status":     status,
		"deleted_at": bson.M{"$exists": false},
		"$expr": bson.M{"$and": bson.A{
			bson.M{"$eq": bson.A{bson.M{"$type": "$status"}, "string"}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$deleted_at"}, "missing"}},
		}},
	}
	total, err := r.mdb.Col().CountDocuments(dbCtx, filter, options.Count().SetMaxTime(time.Until(deadline)))
	if err != nil {
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
	for cur.Next(dbCtx) {
		video, err := decodeCurrentVideo(cur.Current)
		if err != nil {
			return nil, 0, err
		}
		if video == nil || video.ID.IsZero() {
			return nil, 0, ErrVideoReadUnavailable
		}
		videos = append(videos, video)
	}
	if err := cur.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	return videos, total, nil
}
