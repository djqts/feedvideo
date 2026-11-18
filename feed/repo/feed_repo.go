package repo

import (
	"context"
	"errors"
	"time"

	sharedconfig "feedvideo.local/shared-config"
	"feedvideo.local/shared-db/cache"
)

const (
	hotVideosKey = cache.HotVideosKey
)

func poolKey(uid string) string  { return "candidate_pool:" + uid }
func tsKey(uid string) string    { return "feed:pool:ts:" + uid }
func missKey(uid string) string  { return "feed:pool:miss:" + uid }
func lockKey(uid string) string  { return "lock:feed:pool:" + uid }
func retryKey(uid string) string { return "feed:pool:retry:" + uid }
func recKey(uid string) string   { return "recommended:" + uid }

// PoolState 候选池逻辑状态
type PoolState int

const (
	PoolMissing PoolState = iota // 不存在（物理删除或从未构建）
	PoolStale                    // 存在但逻辑过期（用旧池 + 异步重建）
	PoolFresh                    // 新鲜
)

// ScoredVideo 打分后的候选（写入候选池 ZSet）
type ScoredVideo struct {
	ID    string
	Score float64
}

// FeedRepo 封装 feed 所需的全部 Redis 操作（不含索引）
type FeedRepo struct {
	rdb        *cache.RedisClient
	poolTTL    time.Duration
	dedupMax   int64
	dedupTTL   time.Duration
	recallSize int64
}

func NewFeedRepo(rdb *cache.RedisClient, cfg sharedconfig.FeedConfig) *FeedRepo {
	// 防御性默认：若 feed 配置段缺失（全零），零值会导致 EXPIRE 0 立即删池、
	// ZRemRangeByRank 清空去重集等静默严重故障（code review Important #1）。
	if cfg.PoolTTL <= 0 {
		cfg.PoolTTL = 300
	}
	if cfg.RecallSize <= 0 {
		cfg.RecallSize = 200
	}
	if cfg.DedupMax <= 0 {
		cfg.DedupMax = 2000
	}
	if cfg.DedupMax > 2000 {
		cfg.DedupMax = 2000
	}
	if cfg.DedupTTL <= 0 {
		cfg.DedupTTL = 86400
	}
	poolTTL := time.Duration(0)
	if int64(cfg.PoolTTL) <= int64((time.Duration(1<<63-1)/2)/time.Second) {
		poolTTL = time.Duration(cfg.PoolTTL) * time.Second
	}
	dedupTTL := time.Duration(0)
	if int64(cfg.DedupTTL) <= int64(time.Duration(1<<63-1)/time.Second) {
		dedupTTL = time.Duration(cfg.DedupTTL) * time.Second
	}
	return &FeedRepo{
		rdb:        rdb,
		poolTTL:    poolTTL,
		dedupMax:   cfg.DedupMax,
		dedupTTL:   dedupTTL,
		recallSize: cfg.RecallSize,
	}
}

// PoolHasUnseen 遍历池成员计数未看数（#9：不可用 ZCARD 与 seen 大小比较）
func (r *FeedRepo) PoolHasUnseen(ctx context.Context, uid string, seen map[string]bool) (bool, error) {
	members, err := r.PoolWindow(ctx, uid, 499)
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		if !seen[m] {
			return true, nil
		}
	}
	return false, nil
}

func (r *FeedRepo) readReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("feed read context unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return errors.New("feed repository unavailable")
	}
	return nil
}
