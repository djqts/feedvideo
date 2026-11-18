package repo

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ReadCreatorStatsPage preserves the public Feed contract. Missing ordinary
// counters are unknown, not verified zeros. after is an internal cursor.
func (r *VideoRepoMongo) ReadCreatorStatsPage(ctx context.Context, uid, after string, size int64) ([]*model.VideoStats, error) {
	actor, err := strconv.ParseInt(uid, 10, 64)
	if err != nil || actor <= 0 || strconv.FormatInt(actor, 10) != uid || size < 1 || size > 500 {
		return nil, ErrVideoReadInvalid
	}
	var previous primitive.ObjectID
	if after != "" {
		previous, err = primitive.ObjectIDFromHex(after)
		if err != nil || previous.IsZero() || previous.Hex() != after {
			return nil, ErrVideoReadInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, ErrVideoReadUnavailable
	}
	filter := currentLiveVideoFilter()
	filter["author_id"] = uid
	if after != "" {
		filter["_id"] = bson.M{"$gt": previous}
	}
	deadline, _ := ctx.Deadline()
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(size).SetBatchSize(int32(size)).SetMaxTime(time.Until(deadline)).
		SetProjection(bson.M{"_id": 1, "author_id": 1, "status": 1, "deleted_at": 1})
	cursor, err := r.mdb.Col().Find(ctx, filter, opts)
	if err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	defer cursor.Close(ctx)
	ids := make([]string, 0, size)
	for cursor.Next(ctx) {
		if int64(len(ids)) >= size {
			return nil, ErrVideoReadUnavailable
		}
		elements, err := cursor.Current.Elements()
		if err != nil {
			return nil, feedCandidateReadError(ctx, err)
		}
		var id primitive.ObjectID
		idCount, authorCount, statusCount := 0, 0, 0
		for _, element := range elements {
			value := element.Value()
			switch element.Key() {
			case "_id":
				idCount++
				if value.Type != bson.TypeObjectID {
					return nil, ErrVideoReadUnavailable
				}
				id = value.ObjectID()
			case "author_id":
				authorCount++
				if value.Type != bson.TypeString || value.StringValue() != uid {
					return nil, ErrVideoReadUnavailable
				}
			case "status":
				statusCount++
				if value.Type != bson.TypeString || value.StringValue() == "deleted" {
					return nil, ErrVideoReadUnavailable
				}
			case "deleted_at":
				return nil, ErrVideoReadUnavailable
			}
		}
		if idCount != 1 || authorCount != 1 || statusCount != 1 || id.IsZero() || bytes.Compare(id[:], previous[:]) <= 0 {
			return nil, ErrVideoReadUnavailable
		}
		ids = append(ids, id.Hex())
		previous = id
	}
	if err := cursor.Err(); err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	if len(ids) == 0 {
		return []*model.VideoStats{}, nil
	}
	if r.rdb == nil || r.rdb.Client == nil {
		return nil, ErrVideoReadUnavailable
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = fmt.Sprintf("%s:%s", statsCachePrefix, id)
	}
	rows, err := r.boundedOrdinaryStats(ctx, keys)
	if err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	for _, fields := range rows {
		if _, pending := fields["ordinary_stats_bootstrap"]; pending {
			return nil, fmt.Errorf("%w: ordinary stats initialization unconfirmed", ErrVideoReadUnavailable)
		}
	}
	likes, collects, err := r.durableStatsCounts(ctx, ids)
	if err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	result := make([]*model.VideoStats, 0, len(ids))
	for i, id := range ids {
		stats := &model.VideoStats{VideoID: id}
		fields := map[string]*int64{"play_count": &stats.PlayCount, "like_count": &stats.LikeCount, "comment_count": &stats.CommentCount, "share_count": &stats.ShareCount, "collect_count": &stats.CollectCount}
		for field, target := range fields {
			if field == "like_count" && r.durableLikes || field == "collect_count" && r.durableCollections {
				continue
			}
			raw, ok := rows[i][field]
			if !ok {
				return nil, fmt.Errorf("%w: missing %s", ErrVideoReadUnavailable, field)
			}
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 || strconv.FormatInt(value, 10) != raw {
				return nil, fmt.Errorf("%w: invalid %s", ErrVideoReadUnavailable, field)
			}
			*target = value
		}
		if r.durableLikes {
			stats.LikeCount = likes[id]
		}
		if r.durableCollections {
			stats.CollectCount = collects[id]
		}
		if stats.LikeCount < 0 || stats.CollectCount < 0 {
			return nil, ErrVideoReadUnavailable
		}
		result = append(result, stats)
	}
	if err := ctx.Err(); err != nil {
		return nil, feedCandidateReadError(ctx, err)
	}
	return result, nil
}
