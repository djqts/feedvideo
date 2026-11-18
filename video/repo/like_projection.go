package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"feedvideo.local/shared-db/cache"
	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type LikeProjectionLease struct {
	Owner string    `bson:"owner"`
	Token int64     `bson:"token"`
	Until time.Time `bson:"until"`
}

type LikeProjectionStore interface {
	Claim(context.Context, string, time.Time, time.Duration) (*LikeProjectionLease, error)
	Release(context.Context, *LikeProjectionLease) error
	ProjectTarget(context.Context, *LikeProjectionLease, string, string, int64, time.Time) (bool, error)
	ProjectMembership(context.Context, *model.LikeState) error
}

type LikeProjectionRepo struct {
	db  *mongo.Database
	rdb *cache.RedisClient
}

func NewLikeProjectionRepo(db *mongo.Database, rdb *cache.RedisClient) LikeProjectionStore {
	return &LikeProjectionRepo{db: db, rdb: rdb}
}

func (r *LikeProjectionRepo) Claim(ctx context.Context, owner string, now time.Time, ttl time.Duration) (*LikeProjectionLease, error) {
	if owner == "" || ttl <= 0 || ttl > 30*time.Second {
		return nil, ErrLikeInvalid
	}
	if r.db == nil {
		return nil, ErrLikeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now = now.UTC()
	col := r.db.Collection("like_projection_leases")
	// _id is Mongo's unique singleton gate. Concurrent initializers may race only on this upsert.
	_, err := col.UpdateOne(ctx, bson.M{"_id": "global"}, bson.M{"$setOnInsert": bson.M{"owner": "", "token": int64(0), "until": time.Time{}}}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return nil, fmt.Errorf("initialize like projection lease: %w", err)
	}
	var lease LikeProjectionLease
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": "global", "until": bson.M{"$lte": now}}, bson.M{"$inc": bson.M{"token": int64(1)}, "$set": bson.M{"owner": owner, "until": now.Add(ttl)}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&lease)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim like projection lease: %w", err)
	}
	if lease.Owner != owner || lease.Token < 1 || !lease.Until.Equal(now.Add(ttl).Truncate(time.Millisecond)) {
		return nil, fmt.Errorf("invalid claimed like projection lease")
	}
	return &lease, nil
}

func validProjectionLease(lease *LikeProjectionLease) bool {
	return lease != nil && lease.Owner != "" && lease.Token > 0 && !lease.Until.IsZero()
}

func (r *LikeProjectionRepo) Release(ctx context.Context, lease *LikeProjectionLease) error {
	if !validProjectionLease(lease) {
		return ErrLikeInvalid
	}
	if r.db == nil {
		return ErrLikeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.db.Collection("like_projection_leases").UpdateOne(ctx, bson.M{"_id": "global", "owner": lease.Owner, "token": lease.Token}, bson.M{"$set": bson.M{"owner": "", "until": time.Time{}}})
	return err
}

func projectionTargetID(id, kind string) (primitive.ObjectID, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil || oid.Hex() != id || (kind != "video" && kind != "comment") {
		return primitive.NilObjectID, ErrLikeInvalid
	}
	return oid, nil
}

// The comparison is decimal string length/lexical order, never Lua double precision.
const likeProjectionMembershipLua = `
local previous = redis.call('HGET', KEYS[2], ARGV[1])
if previous and (#previous > #ARGV[2] or (#previous == #ARGV[2] and previous > ARGV[2])) then return 0 end
redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])
if ARGV[3] == '1' then redis.call('SADD', KEYS[1], ARGV[1]) else redis.call('SREM', KEYS[1], ARGV[1]) end
return 1
`

func (r *LikeProjectionRepo) ProjectMembership(ctx context.Context, state *model.LikeState) error {
	if state == nil || state.Version < 0 {
		return ErrLikeInvalid
	}
	if _, err := projectionTargetID(state.TargetID, state.TargetType); err != nil {
		return err
	}
	actor, err := strconv.ParseUint(state.ActorID, 10, 64)
	if err != nil || actor == 0 || strconv.FormatUint(actor, 10) != state.ActorID {
		return ErrLikeInvalid
	}
	if r.rdb == nil || r.rdb.Client == nil {
		return ErrLikeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	active := "0"
	if state.Active {
		active = "1"
	}
	suffix := state.TargetType + ":" + state.TargetID
	_, err = r.rdb.EvalContextBounded(ctx, likeProjectionMembershipLua, []string{"likes:" + suffix, "like_versions:" + suffix}, state.ActorID, strconv.FormatInt(state.Version, 10), active)
	return err
}

const likeProjectionCountLua = `
local previous = redis.call('HGET', KEYS[1], 'token')
if previous and (#previous > #ARGV[1] or (#previous == #ARGV[1] and previous > ARGV[1])) then return 0 end
if ARGV[3] == 'video' then
  local statsToken = redis.call('HGET', KEYS[2], 'like_projection_token')
  if statsToken and (#statsToken > #ARGV[1] or (#statsToken == #ARGV[1] and statsToken > ARGV[1])) then return 0 end
end
redis.call('HSET', KEYS[1], 'token', ARGV[1], 'count', ARGV[2])
if ARGV[3] == 'video' then
  redis.call('HSET', KEYS[2], 'like_count', ARGV[2], 'like_projection_token', ARGV[1])
  redis.call('DEL', KEYS[3])
end
return 1
`

func (r *LikeProjectionRepo) ProjectTarget(ctx context.Context, lease *LikeProjectionLease, targetID, targetType string, count int64, now time.Time) (bool, error) {
	if !validProjectionLease(lease) || count < 0 {
		return false, ErrLikeInvalid
	}
	oid, err := projectionTargetID(targetID, targetType)
	if err != nil {
		return false, err
	}
	if !lease.Until.After(now) {
		return false, nil
	}
	if r.db == nil || r.rdb == nil || r.rdb.Client == nil {
		return false, ErrLikeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Verify ownership as well as target fencing: an expired token cannot write an untouched target.
	var actual LikeProjectionLease
	err = r.db.Collection("like_projection_leases").FindOne(ctx, bson.M{"_id": "global", "owner": lease.Owner, "token": lease.Token, "until": bson.M{"$gt": now.UTC()}}).Decode(&actual)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if actual.Owner != lease.Owner || actual.Token != lease.Token || !actual.Until.After(now) {
		return false, nil
	}
	collection := "videos"
	if targetType == "comment" {
		collection = "comments"
	}
	filter := bson.M{"_id": oid, "$or": bson.A{bson.M{"like_projection_token": bson.M{"$exists": false}}, bson.M{"like_projection_token": bson.M{"$lte": lease.Token}}}}
	result, err := r.db.Collection(collection).UpdateOne(ctx, filter, bson.M{"$set": bson.M{"like_count": count, "like_projection_token": lease.Token}})
	if err != nil {
		return false, err
	}
	if result.MatchedCount != 1 {
		return false, nil
	}
	value, err := r.rdb.EvalContextBounded(ctx, likeProjectionCountLua, []string{"like_counts:" + targetType + ":" + targetID, "video_stats:" + targetID, "video:" + targetID}, strconv.FormatInt(lease.Token, 10), strconv.FormatInt(count, 10), targetType)
	if err != nil {
		return false, err
	}
	matched, ok := value.(int64)
	if !ok {
		return false, fmt.Errorf("invalid like count projection result")
	}
	return matched == 1, nil
}
