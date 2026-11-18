package repo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"feedvideo.local/notification/model"
	"feedvideo.local/shared-db/cache"
	mongorepo "feedvideo.local/shared-db/mongo"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ——— Mongo：通知落库 / 列表 / 标记已读 ———

type MongoStore struct {
	mdb *mongorepo.MongoRepo[model.Notification]
}

func NewMongoStore(db *mongo.Database) *MongoStore {
	return &MongoStore{mdb: mongorepo.NewRepository[model.Notification](db, "notifications")}
}

// Insert preserves the legacy caller API; consumers use InsertOnce to detect replay.
func (s *MongoStore) Insert(ctx context.Context, n *model.Notification) error {
	_, err := s.InsertOnce(ctx, n)
	return err
}

func (s *MongoStore) InsertOnce(ctx context.Context, n *model.Notification) (bool, error) {
	n.PayloadHash = notificationHash(n)
	if _, err := s.mdb.InsertOne(ctx, n); err != nil {
		if n.EventID != "" && mongo.IsDuplicateKeyError(err) {
			stored, findErr := s.mdb.FindOne(ctx, bson.M{"event_id": n.EventID, "recipient_id": n.RecipientID})
			if findErr != nil {
				return false, fmt.Errorf("verify duplicate notification (insert: %v): %w", err, findErr)
			}
			// Legacy documents have no hash or source timestamp. Compare their persisted
			// immutable fields; a source timestamp is compared when the replay supplies it.
			if stored.PayloadHash == "" {
				if n.EventCreatedAt != 0 {
					stored.EventCreatedAt = stored.CreatedAt.Unix()
				}
			}
			if notificationHash(stored) != n.PayloadHash {
				return false, fmt.Errorf("%w: event_id=%s recipient_id=%s", model.ErrReplayConflict, n.EventID, n.RecipientID)
			}
			return false, nil
		}
		return false, fmt.Errorf("insert notification: %w", err)
	}
	return true, nil
}

func notificationHash(n *model.Notification) string {
	data, _ := json.Marshal([]any{n.EventID, n.RecipientID, n.Type, n.ActorID, n.TargetID, n.TargetType, n.Content, n.EventCreatedAt})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *MongoStore) ListByRecipient(ctx context.Context, recipient string, page, pageSize int64, typ string, unreadOnly bool) ([]*model.Notification, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 20
	}
	filter := bson.M{"recipient_id": recipient}
	if typ == "interaction" {
		filter["type"] = bson.M{"$in": []string{"like", "comment", "at", "interaction"}}
	} else if typ != "" {
		filter["type"] = typ
	}
	if unreadOnly {
		filter["read"] = false
	}
	total, err := s.mdb.Count(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}
	opts := options.Find().
		SetSort(bson.M{"created_at": -1}).
		SetSkip((page - 1) * pageSize).
		SetLimit(pageSize)
	list, err := s.mdb.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("find notifications: %w", err)
	}
	if list == nil {
		list = []*model.Notification{}
	}
	return list, total, nil
}

func (s *MongoStore) MarkRead(ctx context.Context, recipient string, ids []string, all bool, typ string) (map[string]int64, error) {
	filter := bson.M{"recipient_id": recipient, "read": false}
	if !all {
		objIDs := make([]primitive.ObjectID, 0, len(ids))
		for _, id := range ids {
			oid, err := primitive.ObjectIDFromHex(id)
			if err != nil {
				continue
			}
			objIDs = append(objIDs, oid)
		}
		if len(objIDs) == 0 {
			return map[string]int64{}, nil
		}
		filter["_id"] = bson.M{"$in": objIDs}
	} else if typ != "" {
		filter["type"] = typ
	}

	col := s.mdb.Col()
	cur, err := col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$group", Value: bson.M{"_id": "$type", "n": bson.M{"$sum": 1}}}},
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate mark-read counts: %w", err)
	}
	var rows []struct {
		Type string `bson:"_id"`
		N    int64  `bson:"n"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decode mark-read counts: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.Type] = r.N
	}

	if _, err := col.UpdateMany(ctx, filter, bson.M{"$set": bson.M{"read": true}}); err != nil {
		return nil, fmt.Errorf("update mark-read: %w", err)
	}
	return counts, nil
}

// CountUnreadByType 统计该用户未读通知（按类型），用于 Redis 未读自愈对账。
func (s *MongoStore) CountUnreadByType(ctx context.Context, recipient string) (map[string]int64, error) {
	col := s.mdb.Col()
	cur, err := col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"recipient_id": recipient, "read": false}}},
		{{Key: "$group", Value: bson.M{"_id": "$type", "n": bson.M{"$sum": 1}}}},
	})
	if err != nil {
		return nil, fmt.Errorf("count unread by type: %w", err)
	}
	defer cur.Close(ctx)
	out := map[string]int64{}
	for cur.Next(ctx) {
		var row struct {
			Type string `bson:"_id"`
			N    int64  `bson:"n"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		out[row.Type] = row.N
	}
	return out, cur.Err()
}

var ErrLegacyEventIndex = errors.New("notification legacy global event_id index requires explicit upgrade")

func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	col := s.mdb.Col()
	cur, err := col.Indexes().List(ctx)
	if err != nil {
		return fmt.Errorf("list notification indexes: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var index struct {
			Name   string `bson:"name"`
			Key    bson.D `bson:"key"`
			Unique bool   `bson:"unique"`
		}
		if err := cur.Decode(&index); err != nil {
			return fmt.Errorf("decode notification index: %w", err)
		}
		if index.Unique && len(index.Key) == 1 && index.Key[0].Key == "event_id" {
			return fmt.Errorf("%w: index=%s", ErrLegacyEventIndex, index.Name)
		}
	}
	if err := cur.Err(); err != nil {
		return fmt.Errorf("iterate notification indexes: %w", err)
	}
	eventIdx := mongo.IndexModel{
		Keys: bson.D{{Key: "event_id", Value: 1}, {Key: "recipient_id", Value: 1}},
		Options: options.Index().SetName("notification_event_recipient_unique_v2").SetUnique(true).
			SetPartialFilterExpression(bson.M{"event_id": bson.M{"$type": "string", "$gt": ""}}),
	}
	if _, err := col.Indexes().CreateOne(ctx, eventIdx); err != nil {
		return fmt.Errorf("create notification event-recipient index: %w", err)
	}
	idx := mongo.IndexModel{Keys: bson.D{
		{Key: "recipient_id", Value: 1},
		{Key: "read", Value: 1},
		{Key: "created_at", Value: -1},
	}}
	if _, err := col.Indexes().CreateOne(ctx, idx); err != nil {
		return fmt.Errorf("create notification recipient-read-created index: %w", err)
	}
	return nil
}

// ——— Redis：未读计数 ———

const decrUnreadLua = `
if redis.call('HGET', KEYS[1], '__valid') ~= '1' then return 0 end
local cur = tonumber(redis.call('HGET', KEYS[1], ARGV[1]) or '0')
local dec = tonumber(ARGV[2])
local nv = cur - dec
if nv < 0 then nv = 0 end
redis.call('HSET', KEYS[1], ARGV[1], nv)
return nv
`

const incrUnreadLua = `
if redis.call('HGET', KEYS[1], '__valid') ~= '1' then return 0 end
return redis.call('HINCRBY', KEYS[1], ARGV[1], 1)
`

const setUnreadLua = `
redis.call('DEL', KEYS[1])
redis.call('HSET', KEYS[1], '__valid', '1')
for i = 1, #ARGV, 2 do redis.call('HSET', KEYS[1], ARGV[i], ARGV[i+1]) end
redis.call('PEXPIRE', KEYS[1], 5000)
return 1
`

type RedisUnread struct {
	rdb *cache.RedisClient
}

func NewRedisUnread(rdb *cache.RedisClient) *RedisUnread {
	return &RedisUnread{rdb: rdb}
}

func unreadKey(uid string) string { return "unread:" + uid }

func (u *RedisUnread) IncrUnread(ctx context.Context, uid, typ string) error {
	_, err := u.rdb.Eval(ctx, incrUnreadLua, []string{unreadKey(uid)}, typ)
	return err
}

func (u *RedisUnread) GetUnread(ctx context.Context, uid string) (map[string]int64, error) {
	fields, err := u.rdb.HGetAll(ctx, unreadKey(uid))
	if err != nil {
		return nil, err
	}
	if fields["__valid"] != "1" {
		return nil, nil
	}
	out := make(map[string]int64, len(fields))
	for k, v := range fields {
		if k == "__valid" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid unread cache field %s", k)
		}
		out[k] = n
	}
	return out, nil
}

func (u *RedisUnread) DecrUnread(ctx context.Context, uid, typ string, n int64) error {
	if n <= 0 {
		return nil
	}
	_, err := u.rdb.Eval(ctx, decrUnreadLua, []string{unreadKey(uid)}, typ, n)
	return err
}

// SetAll 整体覆盖该用户的未读计数（对账自愈用，消除两步漂移与负数）。
func (u *RedisUnread) SetAll(ctx context.Context, uid string, counts map[string]int64) error {
	args := make([]any, 0, len(counts)*2)
	for typ, n := range counts {
		if n > 0 {
			args = append(args, typ, n)
		}
	}
	_, err := u.rdb.Eval(ctx, setUnreadLua, []string{unreadKey(uid)}, args...)
	return err
}

func (u *RedisUnread) Invalidate(ctx context.Context, uid string) error {
	return u.rdb.Del(ctx, unreadKey(uid))
}
