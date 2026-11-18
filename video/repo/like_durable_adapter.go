package repo

import (
	"context"
	"errors"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type durableLikeAdapter struct{ store LikeStateStore }

func NewDurableLikeAdapter(store LikeStateStore) LikeRepo { return &durableLikeAdapter{store: store} }

// DurableLikesEnabled lets consumers preserve strict source-read errors.
func (r *durableLikeAdapter) DurableLikesEnabled() bool { return true }

// Legacy writes lack persisted notification intent; durable mode uses the
// state service, which prepares the event before committing the relation.
func (r *durableLikeAdapter) Like(context.Context, string, string, string) error {
	return ErrLikeDurableWriteDisabled
}
func (r *durableLikeAdapter) Unlike(context.Context, string, string, string) error {
	return ErrLikeDurableWriteDisabled
}
func (r *durableLikeAdapter) SyncUserLikes(context.Context, string) error {
	return ErrLikeLegacySyncDisabled
}
func (r *durableLikeAdapter) IsLiked(ctx context.Context, actorID, targetID, targetType string) (bool, error) {
	if r.store == nil {
		return false, likeUnavailable(errors.New("like state store not configured"))
	}
	s, err := r.store.GetLikeState(ctx, model.LikeKey{ActorID: actorID, TargetID: targetID, TargetType: targetType})
	if err != nil {
		return false, err
	}
	if s == nil {
		return false, likeUnavailable(errors.New("like state store returned no state"))
	}
	return s.Active, nil
}
func (r *durableLikeAdapter) GetLikeCount(ctx context.Context, targetID, targetType string) (int64, error) {
	if r.store == nil {
		return 0, likeUnavailable(errors.New("like state store not configured"))
	}
	return r.store.CountActive(ctx, targetID, targetType)
}
func (r *durableLikeAdapter) GetLikeCountFromDB(ctx context.Context, targetID, targetType string) (int64, error) {
	return r.GetLikeCount(ctx, targetID, targetType)
}

type durableLikeQueries interface {
	getLikedUsers(context.Context, string, string, int64, int64) ([]string, error)
	batchLikeCounts(context.Context, []string, string) (map[string]int64, error)
}

func (r *durableLikeAdapter) GetLikedUsers(ctx context.Context, targetID, targetType string, start, stop int64) ([]string, error) {
	queries, ok := r.store.(durableLikeQueries)
	if !ok {
		return nil, likeUnavailable(errors.New("like state store does not support user queries"))
	}
	return queries.getLikedUsers(ctx, targetID, targetType, start, stop)
}
func (r *durableLikeAdapter) BatchGetLikeCountsFromDB(ctx context.Context, ids []string, targetType string) (map[string]int64, error) {
	queries, ok := r.store.(durableLikeQueries)
	if ok {
		return queries.batchLikeCounts(ctx, ids, targetType)
	}
	if r.store == nil {
		return nil, likeUnavailable(errors.New("like state store not configured"))
	}
	if len(ids) > likePageLimit {
		return nil, ErrLikeInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := make(map[string]int64, len(ids))
	for _, id := range ids {
		n, err := r.store.CountActive(ctx, id, targetType)
		if err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, nil
}

func (r *likeStateRepo) getLikedUsers(ctx context.Context, targetID, targetType string, start, stop int64) ([]string, error) {
	if err := validateLikeTarget(targetID, targetType); err != nil {
		return nil, err
	}
	if start < 0 || stop < start {
		return nil, ErrLikeInvalid
	}
	if r == nil || r.col == nil {
		return nil, likeUnavailable(errors.New("Mongo like store not configured"))
	}
	limit := int64(likePageLimit)
	if stop-start < limit {
		limit = stop - start + 1
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cur, err := r.col.Find(ctx, bson.M{"target_id": targetID, "target_type": targetType, "active": true}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(start).SetLimit(limit).SetProjection(bson.M{"actor_id": 1}))
	if err != nil {
		return nil, likeUnavailable(err)
	}
	defer cur.Close(ctx)
	users := []string{}
	for cur.Next(ctx) {
		var row struct {
			ActorID string `bson:"actor_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, likeUnavailable(err)
		}
		users = append(users, row.ActorID)
	}
	if err := cur.Err(); err != nil {
		return nil, likeUnavailable(err)
	}
	return users, nil
}

func (r *likeStateRepo) batchLikeCounts(ctx context.Context, ids []string, targetType string) (map[string]int64, error) {
	if len(ids) > likePageLimit {
		return nil, ErrLikeInvalid
	}
	out := make(map[string]int64, len(ids))
	for _, id := range ids {
		if err := validateLikeTarget(id, targetType); err != nil {
			return nil, err
		}
		out[id] = 0
	}
	if len(ids) == 0 {
		return out, nil
	}
	if r == nil || r.col == nil {
		return nil, likeUnavailable(errors.New("Mongo like store not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cur, err := r.col.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"target_id": bson.M{"$in": ids}, "target_type": targetType, "active": true}}},
		bson.D{{Key: "$group", Value: bson.M{"_id": "$target_id", "n": bson.M{"$sum": 1}}}},
	})
	if err != nil {
		return nil, likeUnavailable(err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
			N  int64  `bson:"n"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, likeUnavailable(err)
		}
		out[row.ID] = row.N
	}
	if err := cur.Err(); err != nil {
		return nil, likeUnavailable(err)
	}
	return out, nil
}
