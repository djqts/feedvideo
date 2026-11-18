package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	"feedvideo.local/shared-db/cache"
	"feedvideo.local/shared-db/elastic"
	mongorepo "feedvideo.local/shared-db/mongo"
	"feedvideo.local/video/model"

	"github.com/redis/go-redis/v9"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	videoCachePrefix  = "video"
	statsCachePrefix  = "video_stats"
	hotVideosKey      = cache.HotVideosKey
	dirtyStatsKey     = "stats:dirty"
	deletedVideosSet  = "deleted_videos"
	hotCommentsPrefix = "hot_comments"
	videoCacheTTL     = 30 * time.Minute
	hotCommentsMaxLen = 200
	hotVideosMax      = int64(100)
)

// esIndexSem 限制异步 ES 索引的最大并发，避免突发写入打爆 ES（有界化）。
var esIndexSem = make(chan struct{}, 8)

type VideoRepo interface {
	Create(ctx context.Context, video *model.Video) error
	GetByID(ctx context.Context, id string) (*model.Video, error)
	BatchGetVideos(ctx context.Context, ids []string) (map[string]*model.Video, error)
	Update(ctx context.Context, id string, update bson.M) error
	SoftDelete(ctx context.Context, id string) error

	GetStats(ctx context.Context, videoID string) (*model.VideoStats, error)
	IncrementStat(ctx context.Context, videoID, field string, delta int64) error
	SyncStatsToDB(ctx context.Context, videoID string) error

	GetHotVideos(ctx context.Context, start, stop int64) ([]*model.Video, error)
	AddHotScore(ctx context.Context, videoID string, score float64) error

	SearchByTags(ctx context.Context, tags []string, page, pageSize int64) ([]*model.Video, int64, error)
	SearchByES(ctx context.Context, keyword string, page, pageSize int64) ([]*model.Video, int64, error)

	UpdateStatus(ctx context.Context, id, status string) error

	List(ctx context.Context, filter bson.M, page, pageSize int64) ([]*model.Video, int64, error)

	// Feed 支撑（feed-flow）
	ListByAuthors(ctx context.Context, authorIDs []string, page, size int64) ([]*model.Video, error)
	GeoSearch(ctx context.Context, lng, lat, radiusMeters float64, limit int64) ([]*model.Video, error)
	BatchGetStats(ctx context.Context, ids []string) (map[string]*model.VideoStats, error)
	BatchGetStatsFromDB(ctx context.Context, ids []string) (map[string]*model.VideoStats, error)
	EnsureIndexes(ctx context.Context) error
}

type VideoRepoMongo struct {
	mdb                     *mongorepo.MongoRepo[model.Video]
	rdb                     *cache.RedisClient
	esClient                *elastic.ESRepo
	durableLikes            bool
	durableCollections      bool
	searchProjectionEnabled bool
	searchProjectionIndex   string
}

// SetDurableLikes fences every legacy stat write for this configured instance.
func (r *VideoRepoMongo) SetDurableLikes(enabled bool) { r.durableLikes = enabled }

// SetDurableCollections keeps Mongo relations authoritative for collection reads
// and fences ordinary cache-driven writes to the independent collect_count.
func (r *VideoRepoMongo) SetDurableCollections(enabled bool) { r.durableCollections = enabled }

func NewVideoRepoMongo(db *mongo.Database, rdb *cache.RedisClient, esClient *elastic.ESRepo) VideoRepo {
	return &VideoRepoMongo{
		mdb:      mongorepo.NewRepository[model.Video](db, "videos"),
		rdb:      rdb,
		esClient: esClient,
	}
}

// ——— CRUD ———

func (r *VideoRepoMongo) Create(ctx context.Context, video *model.Video) error {
	ctx, entryCancel := context.WithTimeout(ctx, 5*time.Second)
	defer entryCancel()
	if video == nil {
		return ErrPublishConflict
	}
	video.ID = primitive.NewObjectID()
	now := time.Now()
	video.CreatedAt = now
	video.UpdatedAt = now
	if video.Status == "" {
		video.Status = "pending"
	}
	InitSearchProjection(video, now)
	ticket, bootstrapErr := r.prepareOrdinaryStatsBootstrap(ctx, video.ID.Hex())
	if bootstrapErr != nil {
		slog.Warn("new video statistics preparation unavailable", "video_id", video.ID.Hex())
	}

	likeTicket := r.prepareVideoLikes(ctx, video.ID.Hex())
	err := r.insertNewVideo(ctx, video)
	if err != nil {
		return fmt.Errorf("insert video: %w", err)
	}
	if likeTicket != "" {
		if err := r.completeVideoLikes(ctx, video.ID.Hex(), likeTicket); err != nil {
			slog.Warn("committed video likes initialization unavailable", "video_id", video.ID.Hex())
		}
	}
	if ticket != "" {
		if err := r.completeOrdinaryStatsBootstrap(ctx, video.ID.Hex(), ticket); err != nil {
			slog.Warn("new video statistics initialization unconfirmed", "video_id", video.ID.Hex())
		}
	}

	// Optional cache writes only follow a committed Mongo insert.
	if r.rdb != nil {
		// 写 Redis 缓存
		cacheKey := fmt.Sprintf("%s:%s", videoCachePrefix, video.ID.Hex())
		data, _ := json.Marshal(video)
		if err := r.rdb.Set(ctx, cacheKey, data, videoCacheTTL); err != nil {
			slog.Warn("cache set failed", "video_id", video.ID.Hex(), "error", err)
		}

	}

	// 异步索引到 ES（有界并发 + 重试）
	if !r.searchProjectionEnabled {
		go r.spawnES("index", func() error { return r.indexToES(video) })
	}

	return nil
}

func (r *VideoRepoMongo) GetByID(ctx context.Context, id string) (*model.Video, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("%s:%s", videoCachePrefix, id)
	// Durable recipient/visibility checks always consult the Mongo document.
	if !r.durableLikes && !r.durableCollections && r.rdb != nil {
		// 检查是否已删除
		isMember, _ := r.rdb.SIsMember(ctx, deletedVideosSet, id)
		if isMember {
			return nil, fmt.Errorf("video deleted")
		}

		// 先查 Redis 缓存
		cached, err := r.rdb.Get(ctx, cacheKey)
		if err == nil && cached != "" {
			var video model.Video
			if err := json.Unmarshal([]byte(cached), &video); err == nil && video.ContentVersion == 0 {
				return &video, nil
			}
		}
	}

	// 查 MongoDB
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid id: %w", err)
	}
	video, err := r.mdb.FindOne(ctx, bson.M{"_id": objID, "status": bson.M{"$ne": "deleted"}, "deleted_at": bson.M{"$exists": false}})
	if err != nil {
		return nil, err
	}

	// 写回缓存
	if r.rdb != nil && !r.durableLikes && !r.durableCollections {
		data, _ := json.Marshal(video)
		_ = r.rdb.Set(ctx, cacheKey, data, videoCacheTTL)
	}

	return video, nil
}

// BatchGetVideos 批量取视频：Redis MGet 命中优先，未命中回源 Mongo $in 并写回缓存。
// Redis 不可用时整体回源 Mongo，保证读链路可用（降级语义）。
func (r *VideoRepoMongo) BatchGetVideos(ctx context.Context, ids []string) (map[string]*model.Video, error) {
	out := make(map[string]*model.Video, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if r.rdb == nil {
		return r.batchGetVideosFromMongo(ctx, ids)
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = fmt.Sprintf("%s:%s", videoCachePrefix, id)
	}
	vals, err := r.rdb.Client.MGet(ctx, keys...).Result()
	if err != nil {
		slog.Warn("batch get videos cache failed, fallback to mongo", "error", err)
		return r.batchGetVideosFromMongo(ctx, ids)
	}
	var missing []string
	for i, v := range vals {
		id := ids[i]
		if v == nil {
			missing = append(missing, id)
			continue
		}
		str, ok := v.(string)
		if !ok {
			missing = append(missing, id)
			continue
		}
		var video model.Video
		if err := json.Unmarshal([]byte(str), &video); err != nil {
			missing = append(missing, id)
			continue
		}
		if video.ContentVersion > 0 {
			missing = append(missing, id)
			continue
		}
		out[id] = &video
	}
	if len(missing) > 0 {
		fromMongo, err := r.batchGetVideosFromMongo(ctx, missing)
		if err != nil {
			return nil, err
		}
		for id, v := range fromMongo {
			out[id] = v
			data, _ := json.Marshal(v)
			_ = r.rdb.Set(ctx, fmt.Sprintf("%s:%s", videoCachePrefix, id), data, videoCacheTTL)
		}
	}
	// 与 GetByID 语义对齐：批量路径同样过滤已删除标记，防止缓存残留的已删视频出现在 Feed
	if len(out) > 0 {
		idsToCheck := make([]string, 0, len(out))
		for id := range out {
			idsToCheck = append(idsToCheck, id)
		}
		pipe := r.rdb.Client.Pipeline()
		cmds := make(map[string]*redis.BoolCmd, len(idsToCheck))
		for _, id := range idsToCheck {
			cmds[id] = pipe.SIsMember(ctx, deletedVideosSet, id)
		}
		if _, err := pipe.Exec(ctx); err == nil {
			for id, cmd := range cmds {
				if deleted, _ := cmd.Result(); deleted {
					delete(out, id)
				}
			}
		}
	}
	return out, nil
}

func (r *VideoRepoMongo) batchGetVideosFromMongo(ctx context.Context, ids []string) (map[string]*model.Video, error) {
	out := make(map[string]*model.Video, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	objIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			continue
		}
		objIDs = append(objIDs, oid)
	}
	if len(objIDs) == 0 {
		return out, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	found, err := r.mdb.Find(dbCtx, bson.M{"_id": bson.M{"$in": objIDs}, "status": bson.M{"$ne": "deleted"}, "deleted_at": bson.M{"$exists": false}})
	cancel()
	if err != nil {
		return nil, fmt.Errorf("batch get videos mongo: %w", err)
	}
	for _, v := range found {
		out[v.ID.Hex()] = v
	}
	return out, nil
}

func (r *VideoRepoMongo) Update(ctx context.Context, id string, update bson.M) error {
	for key := range update {
		if key == "version" || strings.HasPrefix(key, "version.") || key == "search_projection" || strings.HasPrefix(key, "search_projection.") {
			return fmt.Errorf("protected search source field")
		}
	}
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	update = maps.Clone(update)
	if update == nil {
		update = bson.M{}
	}
	now := time.Now().UTC()
	update["updated_at"] = now
	filter := mediaLiveFilter()
	filter["_id"] = objID
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	result, err := r.mdb.Col().UpdateOne(dbCtx, SearchVersionFilter(filter), SearchSourcePipeline(update, nil, now))
	cancel()
	if err != nil {
		return fmt.Errorf("update video: %w", err)
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}

	// 删除缓存
	cacheKey := fmt.Sprintf("%s:%s", videoCachePrefix, id)
	if r.rdb != nil {
		if err := deleteWithRetry(ctx, cacheKey, r.rdb.Del); err != nil {
			slog.Error("cache delete failed after retries", "video_id", id, "error", err)
		}
	}

	// 异步更新 ES
	r.asyncUpdateES(id, update)

	return nil
}

func (r *VideoRepoMongo) SoftDelete(ctx context.Context, id string) error {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	now := time.Now()
	unfinished := bson.M{"$in": bson.A{"$media_job.status", bson.A{"queued", "retry_wait", "processing"}}}
	cancelled := bson.M{"$mergeObjects": bson.A{"$media_job", bson.M{"status": "cancelled", "lease_owner": "", "lease_until": now, "updated_at": now}}}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	result, err := r.mdb.Col().UpdateOne(dbCtx, SearchVersionFilter(bson.M{"_id": objID}), searchSourcePipeline(bson.M{"status": "deleted", "updated_at": now}, nil, bson.M{
		"deleted_at":   bson.M{"$ifNull": bson.A{"$deleted_at", now}},
		"media_job":    bson.M{"$cond": bson.A{unfinished, cancelled, "$media_job"}},
		"media_status": bson.M{"$cond": bson.A{unfinished, "cancelled", "$media_status"}},
	}, now))
	cancel()
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}

	// 清除缓存
	if r.rdb != nil {
		_ = r.rdb.SAdd(ctx, deletedVideosSet, id)
		cacheKey := fmt.Sprintf("%s:%s", videoCachePrefix, id)
		if err := deleteWithRetry(ctx, cacheKey, r.rdb.Del); err != nil {
			slog.Error("cache delete failed after retries", "video_id", id, "error", err)
		}
		_ = r.rdb.ZRem(ctx, hotVideosKey, id)
	}

	// 异步更新 ES（标记删除，有界并发 + 重试）
	if !r.searchProjectionEnabled {
		go r.spawnES("delete", func() error { return r.deleteES(context.Background(), id) })
	}

	return nil
}

// ——— Stats ———

// Reuse the existing owned-connection cancellation boundary for these read-only
// Lua calls. A shared go-redis pool otherwise ignores short caller deadlines at
// the socket. Legacy-only instances retain their existing hash read path.
const ordinaryStatsReadScript = `local rows = {}; for i, key in ipairs(KEYS) do rows[i] = redis.call('HGETALL', key) end; return rows`

func decodeOrdinaryStatsFields(value interface{}) (map[string]string, error) {
	values, ok := value.([]interface{})
	if !ok || len(values)%2 != 0 {
		return nil, fmt.Errorf("invalid stats cache reply")
	}
	fields := make(map[string]string, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, keyOK := values[i].(string)
		value, valueOK := values[i+1].(string)
		if !keyOK || !valueOK {
			return nil, fmt.Errorf("invalid stats cache field")
		}
		fields[key] = value
	}
	return fields, nil
}

func (r *VideoRepoMongo) boundedOrdinaryStats(ctx context.Context, keys []string) ([]map[string]string, error) {
	value, err := r.rdb.EvalContextBounded(ctx, ordinaryStatsReadScript, keys)
	if err != nil {
		return nil, err
	}
	rows, ok := value.([]interface{})
	if !ok || len(rows) != len(keys) {
		return nil, fmt.Errorf("invalid batch stats cache reply")
	}
	fields := make([]map[string]string, len(rows))
	for i, row := range rows {
		fields[i], err = decodeOrdinaryStatsFields(row)
		if err != nil {
			return nil, err
		}
	}
	return fields, nil
}

func (r *VideoRepoMongo) ordinaryStatsFields(ctx context.Context, key string) (map[string]string, error) {
	if !r.durableLikes && !r.durableCollections {
		return r.rdb.HGetAll(ctx, key)
	}
	rows, err := r.boundedOrdinaryStats(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}

func (r *VideoRepoMongo) GetStats(ctx context.Context, videoID string) (*model.VideoStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	statsKey := fmt.Sprintf("%s:%s", statsCachePrefix, videoID)
	var fields map[string]string
	if r.rdb != nil {
		var err error
		fields, err = r.ordinaryStatsFields(ctx, statsKey)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || !r.durableLikes && !r.durableCollections) {
			return nil, fmt.Errorf("get stats: %w", err)
		}
	}

	stats := &model.VideoStats{VideoID: videoID}
	if v, ok := fields["play_count"]; ok {
		stats.PlayCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := fields["like_count"]; ok {
		stats.LikeCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := fields["comment_count"]; ok {
		stats.CommentCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := fields["collect_count"]; ok {
		stats.CollectCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := fields["share_count"]; ok {
		stats.ShareCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if r.durableLikes {
		count, err := r.durableLikeCount(ctx, videoID)
		if err != nil {
			return nil, err
		}
		stats.LikeCount = count
	}
	if r.durableCollections {
		count, err := r.durableCollectCount(ctx, videoID)
		if err != nil {
			return nil, err
		}
		stats.CollectCount = count
	}
	return stats, nil
}

func (r *VideoRepoMongo) durableCollectCount(ctx context.Context, videoID string) (int64, error) {
	if r.mdb == nil {
		return 0, ErrCollectUnavailable
	}
	return NewCollectRepoMongo(r.mdb.Col().Database(), nil).GetCollectCount(ctx, videoID)
}

func (r *VideoRepoMongo) durableLikeCount(ctx context.Context, videoID string) (int64, error) {
	if r.mdb == nil {
		return 0, ErrLikeUnavailable
	}
	return NewLikeStateRepo(r.mdb.Col().Database()).CountActive(ctx, videoID, "video")
}

func (r *VideoRepoMongo) IncrementStat(ctx context.Context, videoID, field string, delta int64) error {
	if r.durableCollections && field == "collect_count" {
		return ErrCollectUnavailable
	}
	if r.durableLikes && field == "like_count" {
		return ErrLikeUnavailable
	}
	if r.rdb == nil {
		return fmt.Errorf("increment stat: Redis unavailable")
	}
	if err := r.incrementOrdinaryStat(ctx, videoID, field, delta); err != nil {
		return err
	}

	// 同时更新热度分
	score := float64(delta)
	switch field {
	case "play_count":
		score = float64(delta) * 1.0
	case "like_count":
		score = float64(delta) * 2.0
	case "comment_count":
		score = float64(delta) * 3.0
	case "collect_count":
		score = float64(delta) * 3.0
	case "share_count":
		score = float64(delta) * 4.0
	}
	_, _ = r.rdb.ZIncrBy(ctx, hotVideosKey, videoID, score)

	return nil
}

func (r *VideoRepoMongo) SyncStatsToDB(ctx context.Context, videoID string) error {
	return r.syncOrdinaryStats(ctx, videoID)
}

func publicHotVideoFilter() bson.M {
	return bson.M{
		"status":     "approved",
		"deleted_at": bson.M{"$exists": false},
		"$expr": bson.M{"$and": bson.A{
			bson.M{"$eq": bson.A{bson.M{"$type": "$status"}, "string"}},
			bson.M{"$in": bson.A{bson.M{"$type": "$media_status"}, bson.A{"missing", "string"}}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$deleted_at"}, "missing"}},
		}},
		"$or": []bson.M{
			{"media_status": "succeeded"},
			{"media_status": ""},
			{"media_status": bson.M{"$exists": false}},
		},
	}
}

func (r *VideoRepoMongo) GetHotVideos(ctx context.Context, start, stop int64) ([]*model.Video, error) {
	if start < 0 || stop < start || stop > 99 {
		return nil, ErrVideoReadInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if r == nil {
		return nil, ErrVideoReadUnavailable
	}
	limit := stop - start + 1
	fallback := func() ([]*model.Video, error) {
		videos, _, err := r.ListPublicVideos(ctx, 1, limit)
		return videos, err
	}
	if r.rdb == nil || r.rdb.Client == nil {
		return fallback()
	}
	reply, err := r.rdb.EvalContextBounded(ctx, "return redis.call('ZREVRANGE', KEYS[1], ARGV[1], ARGV[2])", []string{hotVideosKey}, start, stop)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, ctxErr)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if err != nil {
		return fallback()
	}
	values, ok := reply.([]interface{})
	if !ok || int64(len(values)) > limit {
		return nil, ErrVideoReadUnavailable
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		rawID, ok := value.(string)
		if !ok {
			return nil, ErrVideoReadUnavailable
		}
		oid, err := primitive.ObjectIDFromHex(rawID)
		if err != nil || oid.IsZero() {
			continue
		}
		id := oid.Hex()
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []*model.Video{}, nil
	}
	videoMap, err := r.BatchGetCurrentVideos(ctx, ids)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if videoMap == nil {
		return nil, ErrVideoReadUnavailable
	}
	for id, video := range videoMap {
		if _, wanted := seen[id]; !wanted || video == nil || video.ID.IsZero() || video.ID.Hex() != id {
			return nil, ErrVideoReadUnavailable
		}
	}
	videos := make([]*model.Video, 0, len(ids))
	for _, id := range ids {
		if v, ok := videoMap[id]; ok && model.IsPubliclyVisible(v) {
			videos = append(videos, v)
		}
	}
	return videos, nil
}

func (r *VideoRepoMongo) AddHotScore(ctx context.Context, videoID string, score float64) error {
	_, err := r.rdb.ZIncrBy(ctx, hotVideosKey, videoID, score)
	return err
}

// ——— Search ———

func (r *VideoRepoMongo) SearchByTags(ctx context.Context, tags []string, page, pageSize int64) ([]*model.Video, int64, error) {
	if page < 1 || page > 100 || pageSize < 1 || pageSize > 200 {
		return nil, 0, ErrVideoReadInvalid
	}
	return r.SearchTagCandidates(ctx, tags, (page-1)*pageSize, pageSize)
}

func (r *VideoRepoMongo) SearchByES(ctx context.Context, keyword string, page, pageSize int64) ([]*model.Video, int64, error) {
	if page < 1 || page > 100 || pageSize < 1 || pageSize > 200 {
		return nil, 0, ErrSearchUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	candidates, err := r.SearchCandidates(ctx, keyword, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(candidates.Candidates))
	seen := make(map[string]struct{}, len(candidates.Candidates))
	for _, candidate := range candidates.Candidates {
		oid, err := primitive.ObjectIDFromHex(candidate.ID)
		if err != nil {
			continue
		}
		id := oid.Hex()
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []*model.Video{}, candidates.Total, nil
	}
	current, err := r.BatchGetCurrentVideos(ctx, ids)
	if err != nil {
		return nil, candidates.Total, fmt.Errorf("%w: %w", ErrSearchUnavailable, err)
	}
	videos := make([]*model.Video, 0, len(ids))
	for _, id := range ids {
		if video, ok := current[id]; ok && model.IsPubliclyVisible(video) {
			videos = append(videos, video)
		}
	}
	return videos, candidates.Total, nil
}

// ——— Review ———

func (r *VideoRepoMongo) UpdateStatus(ctx context.Context, id, status string) error {
	return r.Update(ctx, id, bson.M{"status": status})
}

// ——— List ———

func (r *VideoRepoMongo) List(ctx context.Context, filter bson.M, page, pageSize int64) ([]*model.Video, int64, error) {
	if filter == nil {
		filter = bson.M{}
	}
	// 显式 status 过滤优先（如 mediaVisibleFilter 的 status=approved、管理端按状态筛选）；
	// 未指定 status 时排除已删除。注意：filter 为共享 map，仅在此处补默认值。
	if _, ok := filter["status"]; !ok {
		filter["status"] = bson.M{"$ne": "deleted"}
	}

	skip := (page - 1) * pageSize
	opts := options.Find().
		SetSort(bson.M{"created_at": -1}).
		SetSkip(skip).
		SetLimit(pageSize)

	total, err := r.mdb.Count(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	videos, err := r.mdb.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	return videos, total, nil
}

// ——— ES helpers ———

func (r *VideoRepoMongo) asyncUpdateES(id string, update bson.M) {
	if r.searchProjectionEnabled {
		return
	}
	fields := maps.Clone(update)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.spawnES("update", func() error { return r.updateES(ctx, id, fields) })
	}()
}

func (r *VideoRepoMongo) spawnES(op string, fn func() error) {
	if r.searchProjectionEnabled {
		return
	}
	select {
	case esIndexSem <- struct{}{}:
		defer func() { <-esIndexSem }()
		_ = withESRetry(op, fn)
	default:
		slog.Warn("es index saturated, skip", "op", op)
	}
}

// withESRetry 对 ES 写操作做最多 3 次尝试（退避 100ms/200ms/400ms），全部失败返回错误。
func withESRetry(op string, fn func() error) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(time.Duration(100*time.Millisecond) << uint(i))
	}
	slog.Error("es op failed after retries", "op", op, "error", err)
	return err
}

func (r *VideoRepoMongo) indexToES(video *model.Video) error {
	if r.esClient == nil || r.searchProjectionEnabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc := &model.ESSearchDoc{
		VideoID:     video.ID.Hex(),
		Title:       video.Title,
		Description: video.Description,
		Tags:        video.Tags,
		Status:      video.Status,
		CreatedAt:   video.CreatedAt,
	}
	opts := elastic.Options{
		Index:  "videos",
		Type:   "_doc",
		Id:     video.ID.Hex(),
		Source: doc,
	}
	if _, err := r.esClient.Index(ctx, opts); err != nil {
		slog.Warn("es index video failed", "video_id", video.ID.Hex(), "error", err)
		return err
	}
	return nil
}

func (r *VideoRepoMongo) updateES(ctx context.Context, id string, update bson.M) error {
	if r.esClient == nil || r.searchProjectionEnabled {
		return nil
	}
	doc := map[string]interface{}{}
	if title, ok := update["title"]; ok {
		doc["title"] = title
	}
	if desc, ok := update["description"]; ok {
		doc["description"] = desc
	}
	if tags, ok := update["tags"]; ok {
		doc["tags"] = tags
	}
	if status, ok := update["status"]; ok {
		doc["status"] = status
	}
	if len(doc) == 0 {
		return nil
	}
	opts := elastic.Options{Index: "videos", Type: "_doc", Id: id}
	if _, err := r.esClient.Update(ctx, opts, doc); err != nil {
		slog.Warn("es update failed", "video_id", id, "error", err)
		return err
	}
	return nil
}

func (r *VideoRepoMongo) deleteES(ctx context.Context, id string) error {
	if r.esClient == nil || r.searchProjectionEnabled {
		return nil
	}
	opts := elastic.Options{Index: "videos", Type: "_doc", Id: id}
	_, err := r.esClient.Delete(ctx, opts)
	return err
}

// deleteWithRetry 删除缓存最多重试 3 次（间隔 50ms），降低"删除失败导致脏读窗口"的概率。
func deleteWithRetry(ctx context.Context, key string, del func(context.Context, string) error) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = del(ctx, key); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(50 * time.Millisecond):
		}
	}
	return err
}

// durableStatsCounts shares the public operation's deadline across bounded
// source queries. Generic stats callers retain their existing large batches.
func (r *VideoRepoMongo) durableStatsCounts(ctx context.Context, ids []string) (map[string]int64, map[string]int64, error) {
	likes, collects := make(map[string]int64, len(ids)), make(map[string]int64, len(ids))
	if len(ids) == 0 || !r.durableLikes && !r.durableCollections {
		return likes, collects, nil
	}
	if r.mdb == nil {
		if r.durableCollections {
			return nil, nil, ErrCollectUnavailable
		}
		return nil, nil, ErrLikeUnavailable
	}
	if r.durableLikes {
		store := NewDurableLikeAdapter(NewLikeStateRepo(r.mdb.Col().Database()))
		for start := 0; start < len(ids); start += 100 {
			counts, err := store.BatchGetLikeCountsFromDB(ctx, ids[start:min(start+100, len(ids))], "video")
			if err != nil {
				return nil, nil, err
			}
			maps.Copy(likes, counts)
		}
	}
	if r.durableCollections {
		store := NewCollectRepoMongo(r.mdb.Col().Database(), nil)
		for start := 0; start < len(ids); start += 100 {
			counts, err := store.BatchCollectCounts(ctx, ids[start:min(start+100, len(ids))])
			if err != nil {
				return nil, nil, err
			}
			maps.Copy(collects, counts)
		}
	}
	return likes, collects, nil
}

// BatchGetStats reads optional ordinary counters and authoritative configured
// relation counts. Redis failure cannot erase a Mongo collection count.
func (r *VideoRepoMongo) BatchGetStats(ctx context.Context, ids []string) (map[string]*model.VideoStats, error) {
	result := make(map[string]*model.VideoStats, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	durableLikes, durableCollects, err := r.durableStatsCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	cmds := make(map[string]*redis.MapStringStringCmd, len(ids))
	durableFields := make(map[string]map[string]string, len(ids))
	if r.rdb != nil && (r.durableLikes || r.durableCollections) {
		for start := 0; start < len(ids); start += 100 {
			chunk := ids[start:min(start+100, len(ids))]
			keys := make([]string, len(chunk))
			for i, id := range chunk {
				keys[i] = fmt.Sprintf("%s:%s", statsCachePrefix, id)
			}
			rows, err := r.boundedOrdinaryStats(ctx, keys)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				// Socket expiry can precede context's timer. It is still an
				// operation failure, not an optional cache failure to degrade.
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return nil, err
				}
				break // Optional ordinary counters may degrade; relation counts remain authoritative.
			}
			for i, id := range chunk {
				durableFields[id] = rows[i]
			}
		}
	} else if r.rdb != nil {
		pipe := r.rdb.Client.Pipeline()
		for _, id := range ids {
			cmds[id] = pipe.HGetAll(ctx, fmt.Sprintf("%s:%s", statsCachePrefix, id))
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil && !r.durableLikes && !r.durableCollections {
			return nil, fmt.Errorf("batch stats pipeline: %w", err)
		}
	}
	for _, id := range ids {
		fields := durableFields[id]
		if cmd := cmds[id]; cmd != nil {
			fields, _ = cmd.Result()
		}
		stats := &model.VideoStats{VideoID: id}
		if v, ok := fields["play_count"]; ok {
			stats.PlayCount, _ = strconv.ParseInt(v, 10, 64)
		}
		if v, ok := fields["like_count"]; ok {
			stats.LikeCount, _ = strconv.ParseInt(v, 10, 64)
		}
		if r.durableLikes {
			stats.LikeCount = durableLikes[id]
		}
		if v, ok := fields["comment_count"]; ok {
			stats.CommentCount, _ = strconv.ParseInt(v, 10, 64)
		}
		if v, ok := fields["collect_count"]; ok {
			stats.CollectCount, _ = strconv.ParseInt(v, 10, 64)
		}
		if r.durableCollections {
			stats.CollectCount = durableCollects[id]
		}
		if v, ok := fields["share_count"]; ok {
			stats.ShareCount, _ = strconv.ParseInt(v, 10, 64)
		}
		result[id] = stats
	}
	return result, nil
}

// BatchGetStatsFromDB preserves the existing set of present videos, while
// configured relation counters come from their authoritative Mongo collections.
func (r *VideoRepoMongo) BatchGetStatsFromDB(ctx context.Context, ids []string) (map[string]*model.VideoStats, error) {
	out := make(map[string]*model.VideoStats, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r.mdb == nil {
		if r.durableCollections {
			return nil, ErrCollectUnavailable
		}
		if r.durableLikes {
			return nil, ErrLikeUnavailable
		}
		return nil, fmt.Errorf("batch stats from db: Mongo unavailable")
	}
	objIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			objIDs = append(objIDs, oid)
		}
	}
	if len(objIDs) == 0 {
		return out, nil
	}
	cur, err := r.mdb.Col().Find(ctx, bson.M{"_id": bson.M{"$in": objIDs}})
	if err != nil {
		return nil, fmt.Errorf("batch stats from db: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var doc struct {
			ID           primitive.ObjectID `bson:"_id"`
			PlayCount    int64              `bson:"play_count"`
			LikeCount    int64              `bson:"like_count"`
			CommentCount int64              `bson:"comment_count"`
			CollectCount int64              `bson:"collect_count"`
			ShareCount   int64              `bson:"share_count"`
		}
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		id := doc.ID.Hex()
		out[id] = &model.VideoStats{
			VideoID:      id,
			PlayCount:    doc.PlayCount,
			LikeCount:    doc.LikeCount,
			CommentCount: doc.CommentCount,
			CollectCount: doc.CollectCount,
			ShareCount:   doc.ShareCount,
		}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	durableLikes, durableCollects, err := r.durableStatsCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, stats := range out {
		if r.durableLikes {
			stats.LikeCount = durableLikes[id]
		}
		if r.durableCollections {
			stats.CollectCount = durableCollects[id]
		}
	}
	return out, nil
}

// EnsureIndexes 创建 videos 集合所需索引，逐个 best-effort，失败仅 log 不阻塞启动（索引归属 video）。
func (r *VideoRepoMongo) EnsureIndexes(ctx context.Context) error {
	col := r.mdb.Col()
	specs := []struct {
		name  string
		model mongo.IndexModel
	}{
		{"location_2dsphere", mongo.IndexModel{Keys: bson.D{{Key: "location", Value: "2dsphere"}}}},
		{"author_created", mongo.IndexModel{Keys: bson.D{{Key: "author_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		{"author_id_cursor", mongo.IndexModel{Keys: bson.D{{Key: "author_id", Value: 1}, {Key: "_id", Value: 1}}}},
		{"author_id_cursor", mongo.IndexModel{Keys: bson.D{{Key: "author_id", Value: 1}, {Key: "_id", Value: 1}}}},
		{"tags_created", mongo.IndexModel{Keys: bson.D{{Key: "tags", Value: 1}, {Key: "created_at", Value: -1}}}},
		{"created_desc", mongo.IndexModel{Keys: bson.D{{Key: "created_at", Value: -1}}}},
	}
	for _, s := range specs {
		if _, err := col.Indexes().CreateOne(ctx, s.model); err != nil {
			slog.Warn("ensure index failed", "index", s.name, "error", err)
		} else {
			slog.Info("ensured index", "index", s.name)
		}
	}
	return nil
}
