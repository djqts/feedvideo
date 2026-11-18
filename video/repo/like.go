package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"feedvideo.local/shared-db/cache"
	mongorepo "feedvideo.local/shared-db/mongo"
	"feedvideo.local/video/model"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type LikeRepo interface {
	Like(ctx context.Context, userID, targetID, targetType string) error
	Unlike(ctx context.Context, userID, targetID, targetType string) error
	IsLiked(ctx context.Context, userID, targetID, targetType string) (bool, error)
	GetLikedUsers(ctx context.Context, targetID, targetType string, start, stop int64) ([]string, error)
	GetLikeCount(ctx context.Context, targetID, targetType string) (int64, error)
	GetLikeCountFromDB(ctx context.Context, targetID, targetType string) (int64, error)
	BatchGetLikeCountsFromDB(ctx context.Context, ids []string, targetType string) (map[string]int64, error)
	SyncUserLikes(ctx context.Context, userID string) error
}

type LikeRepoMongo struct {
	mdb *mongorepo.MongoRepo[model.LikeRecord]
	rdb *cache.RedisClient
}

// ErrAlreadyLiked 幂等标记：重复点赞不应视为失败
var ErrAlreadyLiked = errors.New("already liked")

func NewLikeRepoMongo(db *mongo.Database, rdb *cache.RedisClient) LikeRepo {
	return &LikeRepoMongo{
		mdb: mongorepo.NewRepository[model.LikeRecord](db, "likes"),
		rdb: rdb,
	}
}

// likeLua 原子化点赞去重：SISMEMBER + SADD 合并为单次原子操作，消除 check-then-act 竞态
// KEYS[1]=likes:{targetType}:{targetID}  ARGV[1]=userID；返回 1=新增点赞，0=已存在
const likeLua = `
if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then
  return 0
end
redis.call('SADD', KEYS[1], ARGV[1])
return 1
`

// likeOutboxKey 点赞持久化 Outbox 队列（Redis List，可靠队列）。
const likeOutboxKey = "likes:outbox"

// likeProcessingKey 点赞事件处理中队列：RPopLPUSH 原子挪入，落库成功再 LRem，崩溃不丢。
const likeProcessingKey = "likes:outbox:processing"

func (r *LikeRepoMongo) Like(ctx context.Context, actor, id, kind string) error {
	changed, err := r.ApplyLegacyLike(ctx, actor, id, kind, true)
	if err != nil {
		return err
	}
	if !changed {
		return ErrAlreadyLiked
	}
	return nil
}

// enqueueLike 将点赞/取消点赞事件写入 Redis Outbox 队列。
func (r *LikeRepoMongo) enqueueLike(ctx context.Context, action, userID, targetID, targetType string) error {
	data, err := json.Marshal(map[string]string{
		"action": action, "user_id": userID, "target_id": targetID, "target_type": targetType,
	})
	if err != nil {
		return err
	}
	return r.rdb.LPush(ctx, likeOutboxKey, string(data))
}

// DrainLikeOutboxOnce 消费 Outbox 中至多 limit 条点赞事件并持久化到 Mongo（去重）。
// 可靠语义：RPopLPUSH 原子挪入 processing 队列，落库成功才 LRem；失败回队重试；
// 开头自愈上次崩溃滞留在 processing 的事件。
func (r *LikeRepoMongo) DrainLikeOutboxOnce(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	// 自愈：processing 中滞留事件挪回主队列
	for {
		raw, err := r.rdb.Client.RPopLPush(ctx, likeProcessingKey, likeOutboxKey).Result()
		if err != nil {
			if err == redis.Nil {
				break
			}
			return 0, fmt.Errorf("recover like processing queue: %w", err)
		}
		if raw == "" {
			break
		}
	}
	drained := 0
	for drained < limit {
		raw, err := r.rdb.Client.RPopLPush(ctx, likeOutboxKey, likeProcessingKey).Result()
		if err != nil {
			if err == redis.Nil {
				break // 队列已空
			}
			return drained, err
		}
		if raw == "" {
			break
		}
		_, drop, perr := processLikeEvent(raw, func(action, userID, targetID, targetType string) error {
			switch action {
			case "unlike":
				return r.removeLikeRecordCtx(ctx, userID, targetID)
			default: // like（兼容旧事件无 action 字段）
				return r.ensureLikeRecord(ctx, userID, targetID, targetType)
			}
		})
		if drop {
			_ = r.rdb.Client.LRem(ctx, likeProcessingKey, 1, raw)
			continue
		}
		if perr != nil {
			slog.Warn("persist like outbox event failed, requeue", "error", perr)
			_ = r.rdb.LPush(ctx, likeOutboxKey, raw)
			break
		}
		_ = r.rdb.Client.LRem(ctx, likeProcessingKey, 1, raw)
		drained++
	}
	return drained, nil
}

// processLikeEvent 解析并分发单条点赞事件；返回 (processed, drop, err)：
// drop=true 表示事件应被丢弃（格式错误/缺字段）；err!=nil 表示落库失败需回队。
func processLikeEvent(raw string, persist func(action, userID, targetID, targetType string) error) (processed, drop bool, err error) {
	var evt struct {
		Action     string `json:"action"`
		UserID     string `json:"user_id"`
		TargetID   string `json:"target_id"`
		TargetType string `json:"target_type"`
	}
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		slog.Warn("drop malformed like outbox event", "error", err)
		return false, true, nil
	}
	if evt.UserID == "" || evt.TargetID == "" {
		slog.Warn("drop like outbox event with empty fields", "raw", raw)
		return false, true, nil
	}
	if err := persist(evt.Action, evt.UserID, evt.TargetID, evt.TargetType); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (r *LikeRepoMongo) Unlike(ctx context.Context, actor, id, kind string) error {
	_, err := r.ApplyLegacyLike(ctx, actor, id, kind, false)
	return err
}

func (r *LikeRepoMongo) IsLiked(ctx context.Context, actor, id, kind string) (bool, error) {
	if kind != "video" || !validLegacyLikeActor(actor) {
		return false, ErrLikeInvalid
	}
	snapshot, err := r.ReadLegacyVideoLikes(ctx, id, actor)
	if err != nil {
		return false, err
	}
	return snapshot.Active, nil
}
func (r *LikeRepoMongo) GetLikedUsers(ctx context.Context, id, kind string, start, stop int64) ([]string, error) {
	if kind != "video" {
		return nil, ErrLikeInvalid
	}
	return r.readVideoLikeMembers(ctx, id)
}
func (r *LikeRepoMongo) GetLikeCount(ctx context.Context, id, kind string) (int64, error) {
	if kind != "video" {
		return 0, ErrLikeInvalid
	}
	snapshot, err := r.ReadLegacyVideoLikes(ctx, id, "")
	if err != nil {
		return 0, err
	}
	return snapshot.Count, nil
}

// GetLikeCountFromDB 从 Mongo 主记录读取点赞数（对账用）。
func (r *LikeRepoMongo) GetLikeCountFromDB(ctx context.Context, targetID, targetType string) (int64, error) {
	counts, err := r.BatchGetLikeCountsFromDB(ctx, []string{targetID}, targetType)
	if err != nil {
		return 0, err
	}
	return counts[targetID], nil
}

// BatchGetLikeCountsFromDB 批量统计 Mongo 点赞记录（对账用）。
func (r *LikeRepoMongo) BatchGetLikeCountsFromDB(ctx context.Context, ids []string, targetType string) (map[string]int64, error) {
	out := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.mdb.Col().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"target_id":   bson.M{"$in": ids},
			"target_type": targetType,
		}}},
		{{Key: "$group", Value: bson.M{"_id": "$target_id", "n": bson.M{"$sum": 1}}}},
	})
	if err != nil {
		return nil, fmt.Errorf("batch like counts from db: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
			N  int64  `bson:"n"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		out[row.ID] = row.N
	}
	return out, cur.Err()
}

// SyncUserLikes 将用户的点赞数据从 Redis 同步到 MongoDB（定时任务调用）
// 扫描所有 likes:* 集合，若用户是成员则确保持久化记录（去重）。
func (r *LikeRepoMongo) SyncUserLikes(ctx context.Context, userID string) error {
	var cursor uint64
	for {
		keys, next, err := r.rdb.Client.Scan(ctx, cursor, "likes:*", 200).Result()
		if err != nil {
			return fmt.Errorf("scan likes sets: %w", err)
		}
		for _, key := range keys {
			parts := strings.Split(key, ":")
			if len(parts) != 3 || parts[0] != "likes" {
				continue
			}
			targetType, targetID := parts[1], parts[2]
			member, err := r.rdb.SIsMember(ctx, key, userID)
			if err != nil || !member {
				continue
			}
			if err := r.ensureLikeRecord(ctx, userID, targetID, targetType); err != nil {
				slog.Warn("sync user like failed", "user_id", userID, "target_id", targetID, "error", err)
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return nil
}

// SyncAllLikes 扫描全部 likes:* 集合，将去重后的点赞用户逐个持久化到 Mongo，
// 返回处理的用户数（供定时任务调用）。
func (r *LikeRepoMongo) SyncAllLikes(ctx context.Context) (int, error) {
	users := make(map[string]struct{})
	var cursor uint64
	for {
		keys, next, err := r.rdb.Client.Scan(ctx, cursor, "likes:*", 200).Result()
		if err != nil {
			return 0, fmt.Errorf("scan likes sets: %w", err)
		}
		for _, key := range keys {
			parts := strings.Split(key, ":")
			if len(parts) != 3 || parts[0] != "likes" {
				continue
			}
			members, err := r.rdb.SMembers(ctx, key)
			if err != nil {
				continue
			}
			for _, u := range members {
				users[u] = struct{}{}
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	for uid := range users {
		if err := r.SyncUserLikes(ctx, uid); err != nil {
			slog.Warn("sync all likes: user failed", "user_id", uid, "error", err)
		}
	}
	return len(users), nil
}

// ensureLikeRecord 以去重方式持久化某条点赞记录。
func (r *LikeRepoMongo) ensureLikeRecord(ctx context.Context, userID, targetID, targetType string) error {
	filter := map[string]interface{}{
		"user_id":     userID,
		"target_id":   targetID,
		"target_type": targetType,
	}
	exists, err := r.mdb.Exists(ctx, filter)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	record := &model.LikeRecord{
		UserID:     userID,
		TargetID:   targetID,
		TargetType: targetType,
		CreatedAt:  time.Now(),
	}
	if _, err := r.mdb.InsertOne(ctx, record); err != nil {
		return err
	}
	return nil
}

// ——— private helpers ———

func (r *LikeRepoMongo) persistLike(userID, targetID, targetType string) {
	ctx := context.Background()
	record := &model.LikeRecord{
		UserID:     userID,
		TargetID:   targetID,
		TargetType: targetType,
		CreatedAt:  time.Now(),
	}
	if _, err := r.mdb.InsertOne(ctx, record); err != nil {
		slog.Warn("persist like failed", "user_id", userID, "target_id", targetID, "error", err)
	}
}

func (r *LikeRepoMongo) removeLikeRecord(userID, targetID string) {
	r.removeLikeRecordCtx(context.Background(), userID, targetID)
}

func (r *LikeRepoMongo) removeLikeRecordCtx(ctx context.Context, userID, targetID string) error {
	filter := map[string]interface{}{
		"user_id":   userID,
		"target_id": targetID,
	}
	_, err := r.mdb.DeleteOne(ctx, filter)
	if err != nil {
		slog.Warn("remove like record failed", "user_id", userID, "target_id", targetID, "error", err)
	}
	return err
}
