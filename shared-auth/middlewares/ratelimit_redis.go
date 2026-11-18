package middlewares

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"feedvideo.local/shared-db/cache"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// rateLimitRedis 全局 Redis 客户端（由 gateway main 注入）。
// 注入后限流从单进程内存升级为 Redis 分布式限流（P2.3）。
var rateLimitRedis atomic.Value // *cache.RedisClient 或 nil

// SetRateLimitRedis 设置分布式限流后端；传 nil 回退内存限流。
func SetRateLimitRedis(rdb *cache.RedisClient) {
	if rdb == nil {
		rateLimitRedis.Store((*cache.RedisClient)(nil))
		return
	}
	rateLimitRedis.Store(rdb)
}

func redisLimiterClient() *cache.RedisClient {
	v := rateLimitRedis.Load()
	if v == nil {
		return nil
	}
	c, _ := v.(*cache.RedisClient)
	return c
}

// incrWithExpiry 原子自增并设置 TTL（仅首次创建时），返回自增后计数。
func incrWithExpiry(ctx context.Context, rdb *cache.RedisClient, key string, window time.Duration) (int64, error) {
	const script = `
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return c
`
	n, err := rdb.Eval(ctx, script, []string{key}, int64(window.Seconds()))
	if err != nil {
		return 0, err
	}
	if v, ok := n.(int64); ok {
		return v, nil
	}
	return 0, errors.New("unexpected Redis rate limit counter type")
}

// RedisRateLimitMiddleware 按维度（默认 IP）限流的分布式实现。
// Redis 不可用时自动降级为内存限流，保证可用性。
func RedisRateLimitMiddleware(dimension string, limit int, interval time.Duration) gin.HandlerFunc {
	mem := RateLimitMiddleware(limit, interval)
	return func(c *gin.Context) {
		rdb := redisLimiterClient()
		if rdb == nil {
			mem(c)
			return
		}
		key := "ratelimit:" + dimension + ":" + c.ClientIP()
		n, err := incrWithExpiry(c.Request.Context(), rdb, key, interval)
		if err != nil {
			mem(c)
			return
		}
		if n > int64(limit) {
			// 动态 Retry-After：按 Redis key 剩余 TTL（内存回退用窗口秒数）
			retryAfter := int(interval.Seconds())
			if pttl, err := rdb.Client.PTTL(c.Request.Context(), key).Result(); err == nil && pttl > 0 {
				retryAfter = int((pttl + time.Second - 1) / time.Second)
				if retryAfter < 1 {
					retryAfter = 1
				}
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "请求过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
}

// UserRateLimitMiddleware 按登录用户维度限流（认证写端点用；无 userID 时回退 IP 维度内存限流）。
func UserRateLimitMiddleware(dimension string, limit int, interval time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(limit, interval)
	mem := func(c *gin.Context, key string) {
		if !limiter.allow(key) {
			c.Header("Retry-After", strconv.Itoa(int(interval.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "请求过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
	return func(c *gin.Context) {
		uidVal, _ := c.Get("userID")
		uid, _ := uidVal.(string)
		if uid == "" {
			mem(c, "ip:"+c.ClientIP())
			return
		}
		rdb := redisLimiterClient()
		if rdb == nil {
			mem(c, "user:"+uid)
			return
		}
		key := "ratelimit:user:" + dimension + ":" + uid
		n, err := incrWithExpiry(c.Request.Context(), rdb, key, interval)
		if err != nil {
			mem(c, "user:"+uid)
			return
		}
		if n > int64(limit) {
			retryAfter := int(interval.Seconds())
			if pttl, err := rdb.Client.PTTL(c.Request.Context(), key).Result(); err == nil && pttl > 0 {
				retryAfter = int((pttl + time.Second - 1) / time.Second)
				if retryAfter < 1 {
					retryAfter = 1
				}
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "请求过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
}

// LoginBackoffLimiter 登录失败渐进式退避（P2.3）：
// - 请求前：失败计数 ≥ 阈值 → 429 + Retry-After；
// - 响应后：401 → 失败计数 +1（带窗口 TTL）并记录审计；200 → 清零。
// 计数键为 ratelimit:loginfail:{ip}[:{account}]，Redis 可用时分布式共享，
// 单实例/Redis 不可用时回退进程内计数（保证可用性）。
func LoginBackoffLimiter(threshold int, window time.Duration) gin.HandlerFunc {
	fails := newMemoryCounter()
	// Failed writes are absent from otherwise readable Redis counters. Retain
	// them separately so READONLY/OOM errors cannot bypass the login backoff.
	failedWrites := newMemoryCounter()
	return func(c *gin.Context) {
		ip := c.ClientIP()
		account := readLoginAccount(c)
		redisKey := "ratelimit:loginfail:" + ip
		if account != "" {
			redisKey += ":" + account
		}
		rdb := redisLimiterClient()
		useRedis := rdb != nil
		before := int64(0)
		if useRedis {
			v, err := rdb.Get(c.Request.Context(), redisKey)
			if err == nil {
				before, err = strconv.ParseInt(v, 10, 64)
			}
			if err != nil && !errors.Is(err, redis.Nil) {
				useRedis = false
			}
		}
		if !useRedis {
			before = fails.get(ip + ":" + account)
		} else {
			before += failedWrites.get(ip + ":" + account)
		}
		if before >= int64(threshold) {
			wait := int(window.Seconds())
			c.Header("Retry-After", strconv.Itoa(wait))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "too many failed login attempts, please retry later",
				"message": "登录失败次数过多，请稍后再试",
			})
			return
		}
		c.Next()

		status := c.Writer.Status()
		switch {
		case status == http.StatusUnauthorized:
			if useRedis {
				if _, err := incrWithExpiry(c.Request.Context(), rdb, redisKey, window); err != nil {
					fails.incr(ip+":"+account, window)
					failedWrites.incr(ip+":"+account, window)
				}
			} else {
				fails.incr(ip+":"+account, window)
			}
			log.Printf("[audit] login failure ip=%s account=%q", c.ClientIP(), account)
		case status == http.StatusOK:
			if rdb != nil {
				_ = rdb.Del(c.Request.Context(), redisKey)
			}
			fails.reset(ip + ":" + account)
			failedWrites.reset(ip + ":" + account)
			log.Printf("[audit] login success ip=%s account=%q", c.ClientIP(), account)
		}
	}
}

// readLoginAccount 读取登录请求体中的 username，同时把 body 原样放回
// （不干扰后续 handler 的 ShouldBindJSON）。JSON 与 form 两种格式都支持。
func readLoginAccount(c *gin.Context) string {
	if v := c.PostForm("username"); v != "" {
		return v
	}
	if c.Request.Body == nil {
		return ""
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var payload struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Username
}

// memoryCounter 无 Redis 时的进程内失败计数（单实例部署兜底）。
type memoryCounter struct {
	mu    sync.Mutex
	store map[string]counterEntry
}

type counterEntry struct {
	count int64
	until int64 // unix 秒
}

func newMemoryCounter() *memoryCounter {
	return &memoryCounter{store: make(map[string]counterEntry)}
}

func (m *memoryCounter) get(key string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.store[key]
	if !ok || time.Now().Unix() >= e.until {
		return 0
	}
	return e.count
}

func (m *memoryCounter) incr(key string, window time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	e, ok := m.store[key]
	if !ok || now >= e.until {
		e = counterEntry{count: 0, until: now + int64(window.Seconds())}
	}
	e.count++
	m.store[key] = e
}

func (m *memoryCounter) reset(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, key)
}
