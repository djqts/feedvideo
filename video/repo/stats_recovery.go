package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"feedvideo.local/video/model"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var ErrStatsSyncConflict = errors.New("ordinary stats changed before persistence confirmation")

const ordinaryStatsRevisionField = "ordinary_stats_revision"

// Fixed fields and keys keep response/work bounded even in a polluted hash.
const ordinaryStatsSampleScript = `
local fields = {}
for i = 2, #ARGV do
 local value = redis.call('HGET', KEYS[1], ARGV[i])
 if value and string.len(value) > 20 then value = false end
 fields[#fields+1] = value or false
end
local token = redis.call('HGET', KEYS[1], 'ordinary_stats_token') or ''
if string.len(token) > 36 then token = '' end
return {redis.call('ZSCORE', KEYS[2], ARGV[1]) or '', fields, token, redis.call('HEXISTS', KEYS[1], 'ordinary_stats_bootstrap')}
`

func (r *VideoRepoMongo) ordinaryWriteFields() []string {
	fields := []string{"play_count"}
	if !r.durableLikes {
		fields = append(fields, "like_count")
	}
	fields = append(fields, "comment_count")
	if !r.durableCollections {
		fields = append(fields, "collect_count")
	}
	return append(fields, "share_count")
}

func (r *VideoRepoMongo) statsUnavailable() error {
	if r.durableCollections {
		return ErrCollectUnavailable
	}
	if r.durableLikes {
		return ErrLikeUnavailable
	}
	return fmt.Errorf("ordinary stats dependency unavailable")
}

func (r *VideoRepoMongo) statsWriteSample(ctx context.Context, id string) (*model.StatsSyncReceipt, error) {
	if r.rdb == nil {
		return nil, r.statsUnavailable()
	}
	fields := r.ordinaryWriteFields()
	args := []interface{}{id}
	for _, field := range fields {
		args = append(args, field)
	}
	reply, err := r.rdb.EvalContextBounded(ctx, ordinaryStatsSampleScript, []string{statsCachePrefix + ":" + id, dirtyStatsKey}, args...)
	if err != nil {
		return nil, fmt.Errorf("read ordinary stats sample: %w", err)
	}
	rows, ok := reply.([]interface{})
	if !ok || len(rows) != 4 {
		return nil, fmt.Errorf("invalid stats sample")
	}
	score, ok := rows[0].(string)
	if !ok {
		return nil, fmt.Errorf("invalid dirty score sample")
	}
	values, ok := rows[1].([]interface{})
	if !ok || len(values) != len(fields) {
		return nil, fmt.Errorf("invalid stats fields sample")
	}
	token, ok := rows[2].(string)
	if !ok {
		return nil, fmt.Errorf("invalid stats token sample")
	}
	if parsed, err := uuid.Parse(token); err != nil || parsed.String() != token {
		token = ""
	}
	pending, ok := rows[3].(int64)
	if !ok || pending < 0 || pending > 1 {
		return nil, fmt.Errorf("invalid stats bootstrap sample")
	}
	receipt := &model.StatsSyncReceipt{VideoID: id, DirtyScore: score, Token: token, Fields: map[string]string{}, Complete: pending == 0}
	for i, field := range fields {
		raw, ok := values[i].(string)
		value, err := strconv.ParseInt(raw, 10, 64)
		if !ok || err != nil || value < 0 {
			receipt.Complete = false
			continue
		}
		receipt.Fields[field] = raw
	}
	return receipt, nil
}

// All ordinary sync entry points use this read-before-sample CAS. A matched
// no-change write is confirmation; a lost CAS or unknown reply is not.
func (r *VideoRepoMongo) SyncStatsForRecovery(ctx context.Context, id string) (*model.StatsSyncReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil || oid.Hex() != id {
		return nil, fmt.Errorf("invalid stats video id")
	}
	if r.mdb == nil || r.rdb == nil {
		return nil, r.statsUnavailable()
	}
	fields := r.ordinaryWriteFields()
	projection := bson.D{{Key: "_id", Value: 1}, {Key: ordinaryStatsRevisionField, Value: 1}}
	for _, field := range fields {
		projection = append(projection, bson.E{Key: field, Value: 1})
	}
	col, err := r.mdb.Col().Clone(options.Collection().SetReadPreference(readpref.Primary()))
	if err != nil {
		return nil, err
	}
	raw, err := col.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().SetProjection(projection)).Raw()
	if err != nil {
		return nil, fmt.Errorf("read stats baseline: %w", err)
	}
	elements, err := raw.Elements()
	if err != nil {
		return nil, err
	}
	baseline := map[string]bson.RawValue{}
	for _, element := range elements {
		name := element.Key()
		if _, exists := baseline[name]; exists {
			return nil, fmt.Errorf("duplicate stats baseline field %s", name)
		}
		baseline[name] = element.Value()
	}
	if value, ok := baseline["_id"]; !ok || value.Type != bsontype.ObjectID || value.ObjectID() != oid {
		return nil, fmt.Errorf("invalid stats baseline identity")
	}
	filter := bson.M{"_id": oid}
	guards := bson.A{}
	var revision int64
	for _, field := range append(fields, ordinaryStatsRevisionField) {
		old, exists := baseline[field]
		if !exists {
			filter[field] = bson.M{"$exists": false}
			continue
		}
		var value int64
		switch old.Type {
		case bsontype.Int32:
			value = int64(old.Int32())
		case bsontype.Int64:
			value = old.Int64()
		default:
			return nil, fmt.Errorf("invalid stats baseline type for %s", field)
		}
		if value < 0 {
			return nil, fmt.Errorf("negative stats baseline for %s", field)
		}
		if field == ordinaryStatsRevisionField {
			if value == math.MaxInt64 {
				return nil, fmt.Errorf("ordinary stats revision exhausted")
			}
			revision = value
		}
		guards = append(guards, bson.M{"$in": bson.A{bson.M{"$type": "$" + field}, bson.A{"int", "long"}}}, bson.M{"$eq": bson.A{"$" + field, value}})
	}
	if len(guards) > 0 {
		filter["$expr"] = bson.M{"$and": guards}
	}
	receipt, err := r.statsWriteSample(ctx, id)
	if err != nil {
		return nil, err
	}
	// Partial counters may have restarted from zero after eviction. Neither a
	// Mongo CAS nor keeping dirty work makes such an absolute write safe.
	if !receipt.Complete {
		return receipt, ErrOrdinaryStatsUnavailable
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	set := bson.M{"updated_at": time.Now(), ordinaryStatsRevisionField: revision + 1}
	for field, raw := range receipt.Fields {
		value, _ := strconv.ParseInt(raw, 10, 64)
		set[field] = value
	}
	result, err := r.mdb.UpdateOne(ctx, filter, bson.M{"$set": set}, options.Update().SetUpsert(false))
	if err != nil {
		return receipt, fmt.Errorf("persist ordinary stats: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if result == nil || result.MatchedCount != 1 || result.UpsertedCount != 0 {
		return receipt, ErrStatsSyncConflict
	}
	receipt.Persisted = true
	return receipt, nil
}

// Keep the public no-op for wholly missing/invalid cache fields, while every
// actual write obtains a fresh sample after reading its Mongo baseline.
func (r *VideoRepoMongo) syncOrdinaryStats(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil || oid.Hex() != id {
		return fmt.Errorf("invalid stats video id")
	}
	sample, err := r.statsWriteSample(ctx, id)
	if err != nil {
		return err
	}
	if len(sample.Fields) == 0 {
		return nil
	}
	_, err = r.SyncStatsForRecovery(ctx, id)
	return err
}
