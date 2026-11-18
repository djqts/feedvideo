package scheduler

import (
	"context"
	"log"
	"time"

	"feedvideo.local/shared-db/cache"
	videomodel "feedvideo.local/video/model"
	videoService "feedvideo.local/video/service"
)

const (
	hotVideosKey        = cache.HotVideosKey
	dirtyStatsKey       = "stats:dirty"
	defaultSyncInterval = 10 * time.Minute
)

// StatsSyncer batches Redis stats into Mongo. *service.VideoService satisfies it.
type StatsSyncer interface {
	BatchSyncStats(ctx context.Context, req *videoService.SyncStatsReq) (int, int, error)
}

// StatsReconciler 提供 Redis 派生统计与 Mongo 主记录统计，供差异对账。
type StatsReconciler interface {
	BatchGetStats(ctx context.Context, ids []string) (map[string]*videomodel.VideoStats, error)
	BatchGetStatsFromDB(ctx context.Context, ids []string) (map[string]*videomodel.VideoStats, error)
}

// LikeAllSyncer syncs all pending likes into Mongo. *repo.LikeRepoMongo satisfies it.
type LikeAllSyncer interface {
	SyncAllLikes(ctx context.Context) (int, error)
}

// LikeOutboxDrainer drains the likes outbox queue into Mongo.
type LikeOutboxDrainer interface {
	DrainLikeOutboxOnce(ctx context.Context, limit int) (int, error)
}

// LikePersister 组合 Outbox 消费与全量补偿。
type LikePersister interface {
	LikeAllSyncer
	LikeOutboxDrainer
}

// LikeReconciler 提供 Redis 实时点赞数与 Mongo 主记录点赞数，供差异对账。
type LikeReconciler interface {
	GetLikeCount(ctx context.Context, targetID, targetType string) (int64, error)
	BatchGetLikeCountsFromDB(ctx context.Context, ids []string, targetType string) (map[string]int64, error)
}

// ReconcileLikeOnce 对热门视频做 Redis↔Mongo 点赞差异对账，返回差异视频数。
func ReconcileLikeOnce(ctx context.Context, rdb *cache.RedisClient, rec LikeReconciler, limit int64) (int, error) {
	if rec == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	ids, err := rdb.ZRevRange(ctx, hotVideosKey, 0, limit-1)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	diff := 0
	mongoCounts, err := rec.BatchGetLikeCountsFromDB(ctx, ids, "video")
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		redisCount, err := rec.GetLikeCount(ctx, id, "video")
		if err != nil {
			continue
		}
		if redisCount != mongoCounts[id] {
			diff++
		}
	}
	return diff, nil
}

// SyncHotVideoStatsOnce syncs stats for the current hot-video list and returns
// (success, failed) counts.
func SyncHotVideoStatsOnce(ctx context.Context, rdb *cache.RedisClient, syncer StatsSyncer) (int, int, error) {
	ids, err := rdb.ZRevRange(ctx, hotVideosKey, 0, 99)
	if err != nil {
		return 0, 0, err
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	return syncer.BatchSyncStats(ctx, &videoService.SyncStatsReq{VideoIDs: ids})
}

// diffStats 统计差异计数：任一字段（play/like/comment/collect/share）不一致即记 1。
func diffStats(redisStats, mongoStats map[string]*videomodel.VideoStats, ids []string) int {
	diff := 0
	for _, id := range ids {
		r, m := redisStats[id], mongoStats[id]
		if r == nil || m == nil {
			continue
		}
		if r.PlayCount != m.PlayCount || r.LikeCount != m.LikeCount ||
			r.CommentCount != m.CommentCount || r.CollectCount != m.CollectCount ||
			r.ShareCount != m.ShareCount {
			diff++
		}
	}
	return diff
}

// ReconcileHotStatsOnce 对热门视频做 Redis↔Mongo 统计差异对账，返回差异视频数。
func ReconcileHotStatsOnce(ctx context.Context, rdb *cache.RedisClient, rec StatsReconciler, limit int64) (int, error) {
	if rec == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	ids, err := rdb.ZRevRange(ctx, hotVideosKey, 0, limit-1)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	redisStats, err := rec.BatchGetStats(ctx, ids)
	if err != nil {
		return 0, err
	}
	mongoStats, err := rec.BatchGetStatsFromDB(ctx, ids)
	if err != nil {
		return 0, err
	}
	return diffStats(redisStats, mongoStats, ids), nil
}

// RunStatsSyncLoop periodically syncs hot-video stats until ctx is cancelled.
// interval <= 0 falls back to the default.
func RunStatsSyncLoop(ctx context.Context, rdb *cache.RedisClient, syncer StatsSyncer, reconciler StatsReconciler, interval time.Duration) {
	if interval <= 0 {
		interval = defaultSyncInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			dirtyOK, dirtyFail, derr := SyncDirtyVideoStatsOnce(runCtx, rdb, syncer, 100)
			if derr != nil {
				log.Printf("[scheduler] dirty stats sync failed: %v", derr)
			} else if dirtyOK+dirtyFail > 0 {
				log.Printf("[scheduler] dirty stats sync: success=%d failed=%d", dirtyOK, dirtyFail)
			}
			success, failed, err := SyncHotVideoStatsOnce(runCtx, rdb, syncer)
			diff, derr := ReconcileHotStatsOnce(runCtx, rdb, reconciler, 20)
			if derr == nil {
				_ = rdb.Set(runCtx, "metrics:reconcile:stats_diff", diff, 5*time.Minute)
				if diff > 0 {
					log.Printf("[scheduler] stats reconcile: %d hot videos have field diffs", diff)
				}
			} else {
				log.Printf("[scheduler] stats reconcile failed: %v", derr)
			}
			cancel()
			if err != nil {
				log.Printf("[scheduler] stats sync failed: %v", err)
				continue
			}
			if success+failed > 0 {
				log.Printf("[scheduler] stats sync: success=%d failed=%d", success, failed)
			}
		}
	}
}

// SyncAllLikesOnce runs one global like-sync pass and returns the number of users processed.
func SyncAllLikesOnce(ctx context.Context, syncer LikeAllSyncer) (int, error) {
	return syncer.SyncAllLikes(ctx)
}

// RunLikeSyncLoop periodically drains the likes outbox then runs a full
// compensation sync until ctx is cancelled.
func RunLikeSyncLoop(ctx context.Context, rdb *cache.RedisClient, syncer LikePersister, reconciler LikeReconciler, interval time.Duration) {
	if interval <= 0 {
		interval = defaultSyncInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			drained, err := syncer.DrainLikeOutboxOnce(runCtx, 500)
			if err != nil {
				cancel()
				log.Printf("[scheduler] like outbox drain failed: %v", err)
				continue
			}
			n, err := SyncAllLikesOnce(runCtx, syncer)
			likeDiff, lerr := ReconcileLikeOnce(runCtx, rdb, reconciler, 20)
			if lerr == nil {
				_ = rdb.Set(runCtx, "metrics:reconcile:like_diff", likeDiff, 5*time.Minute)
				if likeDiff > 0 {
					log.Printf("[scheduler] like reconcile: %d hot videos have count diffs", likeDiff)
				}
			} else {
				log.Printf("[scheduler] like reconcile failed: %v", lerr)
			}
			cancel()
			if err != nil {
				log.Printf("[scheduler] like sync failed: %v", err)
				continue
			}
			if drained > 0 || n > 0 {
				log.Printf("[scheduler] like sync: outbox=%d users=%d", drained, n)
			}
		}
	}
}
