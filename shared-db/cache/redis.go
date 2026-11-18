package cache

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

type RedisClient struct {
	Client *redis.Client
}

func InitRedis(addr, password string, db int) (*RedisClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  1 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		MaxRetries:   1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return &RedisClient{Client: rdb}, nil
}

func (r *RedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return r.Client.Set(ctx, key, value, expiration).Err()
}

func (r *RedisClient) Get(ctx context.Context, key string) (string, error) {
	return r.Client.Get(ctx, key).Result()
}

func (r *RedisClient) Del(ctx context.Context, key string) error {
	return r.Client.Del(ctx, key).Err()
}

// Incr 原子自增（计数器，供指标/限流使用）。
func (r *RedisClient) Incr(ctx context.Context, key string) (int64, error) {
	return r.Client.Incr(ctx, key).Result()
}

// Decr 原子自减。
func (r *RedisClient) Decr(ctx context.Context, key string) (int64, error) {
	return r.Client.Decr(ctx, key).Result()
}

func (r *RedisClient) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.Client.Exists(ctx, key).Result()
	return n > 0, err
}

// ——— Hash operations ———

func (r *RedisClient) HSet(ctx context.Context, key string, fields map[string]interface{}) error {
	return r.Client.HSet(ctx, key, fields).Err()
}

func (r *RedisClient) HGet(ctx context.Context, key, field string) (string, error) {
	return r.Client.HGet(ctx, key, field).Result()
}

func (r *RedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return r.Client.HGetAll(ctx, key).Result()
}

func (r *RedisClient) HIncrBy(ctx context.Context, key, field string, incr int64) (int64, error) {
	return r.Client.HIncrBy(ctx, key, field, incr).Result()
}

func (r *RedisClient) HDel(ctx context.Context, key string, fields ...string) error {
	return r.Client.HDel(ctx, key, fields...).Err()
}

// ——— Sorted Set operations ———

func (r *RedisClient) ZAdd(ctx context.Context, key string, score float64, member string) error {
	return r.Client.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err()
}

func (r *RedisClient) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return r.Client.ZRevRange(ctx, key, start, stop).Result()
}

func (r *RedisClient) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) ([]redis.Z, error) {
	return r.Client.ZRevRangeWithScores(ctx, key, start, stop).Result()
}

// ZRangeByScore 按分数区间返回成员（升序），供统计增量同步等批量取数使用。
func (r *RedisClient) ZRangeByScore(ctx context.Context, key, min, max string, offset, count int64) ([]string, error) {
	return r.Client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min:    min,
		Max:    max,
		Offset: offset,
		Count:  count,
	}).Result()
}

func (r *RedisClient) ZIncrBy(ctx context.Context, key, member string, increment float64) (float64, error) {
	return r.Client.ZIncrBy(ctx, key, increment, member).Result()
}

func (r *RedisClient) ZRem(ctx context.Context, key string, members ...string) error {
	return r.Client.ZRem(ctx, key, members).Err()
}

func (r *RedisClient) ZCard(ctx context.Context, key string) (int64, error) {
	return r.Client.ZCard(ctx, key).Result()
}

// ——— Set operations ———

func (r *RedisClient) SAdd(ctx context.Context, key string, members ...string) error {
	return r.Client.SAdd(ctx, key, members).Err()
}

func (r *RedisClient) SIsMember(ctx context.Context, key, member string) (bool, error) {
	return r.Client.SIsMember(ctx, key, member).Result()
}

func (r *RedisClient) SRem(ctx context.Context, key string, members ...string) error {
	return r.Client.SRem(ctx, key, members).Err()
}

func (r *RedisClient) SMembers(ctx context.Context, key string) ([]string, error) {
	return r.Client.SMembers(ctx, key).Result()
}

func (r *RedisClient) SCard(ctx context.Context, key string) (int64, error) {
	return r.Client.SCard(ctx, key).Result()
}

// ——— List operations ———

func (r *RedisClient) LPush(ctx context.Context, key string, values ...string) error {
	args := make([]interface{}, len(values))
	for i, v := range values {
		args[i] = v
	}
	return r.Client.LPush(ctx, key, args...).Err()
}

// RPop 从列表尾部弹出元素（用于可靠队列消费）。
func (r *RedisClient) RPop(ctx context.Context, key string) (string, error) {
	return r.Client.RPop(ctx, key).Result()
}

func (r *RedisClient) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return r.Client.LRange(ctx, key, start, stop).Result()
}

func (r *RedisClient) LTrim(ctx context.Context, key string, start, stop int64) error {
	return r.Client.LTrim(ctx, key, start, stop).Err()
}

func (r *RedisClient) LLen(ctx context.Context, key string) (int64, error) {
	return r.Client.LLen(ctx, key).Result()
}

func (r *RedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return r.Client.Expire(ctx, key, expiration).Err()
}

// ——— Feed 所需扩展（feed-flow）———

// Eval 执行 Lua 脚本
func (r *RedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return r.Client.Eval(ctx, script, keys, args...).Result()
}

// EvalContextBounded executes projection Lua within the parent's remaining
// deadline, with a five-second ceiling. A short-lived single-connection client
// isolates cancellation from the shared pool: go-redis only applies deadlines
// to socket I/O, so cancellation must close the owned socket as well.
//
// There is no automatic command retry. Any error after a write, including a
// canceled/expired context, is an unknown result; callers must retain durable
// work and retry with its existing version/token. A successful reply preserves
// Eval's result shape. Each call incurs a new connection/handshake.
func (r *RedisClient) EvalContextBounded(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opt := *r.Client.Options()
	opt.ContextTimeoutEnabled = true
	opt.MaxRetries = -1
	opt.PoolSize, opt.MaxActiveConns, opt.MaxConcurrentDials = 1, 1, 1
	opt.MinIdleConns, opt.MaxIdleConns = 0, 1
	// This client owns one command, so it needs no long-lived maintenance or
	// shared push processor. Use a fresh config rather than mutating the parent.
	opt.MaintNotificationsConfig = &maintnotifications.Config{Mode: maintnotifications.ModeDisabled}
	opt.PushNotificationProcessor = nil
	owned := redis.NewClient(&opt)
	var closeOnce sync.Once
	closeOwned := func() { closeOnce.Do(func() { _ = owned.Close() }) }
	stopCancelClose := context.AfterFunc(ctx, closeOwned)
	defer func() {
		stopCancelClose()
		// sync.Once also waits for an already-running cancellation close. There
		// is no detached Eval goroutine or connection returned to the shared pool.
		closeOwned()
	}()
	value, err := owned.Eval(ctx, script, keys, args...).Result()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	// A socket deadline can fire just before the context timer is scheduled.
	if deadline, ok := ctx.Deadline(); err != nil && ok && !time.Now().Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	return value, err
}

// SetNX 仅当 key 不存在时设置，返回是否设置成功（用于分布式锁/冷却）
func (r *RedisClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	return r.Client.SetNX(ctx, key, value, expiration).Result()
}

// ZScore 返回成员分值，成员不存在返回 redis.Nil 错误
func (r *RedisClient) ZScore(ctx context.Context, key, member string) (float64, error) {
	return r.Client.ZScore(ctx, key, member).Result()
}

// ZRemRangeByRank 按排名区间删除成员
func (r *RedisClient) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) error {
	return r.Client.ZRemRangeByRank(ctx, key, start, stop).Err()
}
