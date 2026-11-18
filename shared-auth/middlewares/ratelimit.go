package middlewares

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter is a simple in-memory sliding-window rate limiter.
// For production use, replace with a Redis-backed implementation.
type RateLimiter struct {
	mu       sync.Mutex
	windows  map[string]*windowBuckets
	limit    int
	interval time.Duration
}

type windowBuckets struct {
	counters []int64
	head     int
}

// NewRateLimiter creates a new rate limiter.
// limit: max requests per interval.
// interval: the sliding window duration.
func NewRateLimiter(limit int, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		windows:  make(map[string]*windowBuckets),
		limit:    limit,
		interval: interval,
	}
	// Cleanup expired entries every 5 minutes
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			rl.mu.Lock()
			for k := range rl.windows {
				delete(rl.windows, k)
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now().Unix()
	buckets, ok := rl.windows[key]
	if !ok {
		buckets = &windowBuckets{
			counters: make([]int64, 0, rl.limit),
			head:     0,
		}
		rl.windows[key] = buckets
	}

	// Remove expired entries (older than interval)
	cutoff := now - int64(rl.interval.Seconds())
	valid := make([]int64, 0, len(buckets.counters))
	for _, ts := range buckets.counters {
		if ts > cutoff {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= rl.limit {
		buckets.counters = valid
		buckets.head = len(valid)
		return false
	}

	valid = append(valid, now)
	buckets.counters = valid
	buckets.head = len(valid)
	return true
}

// RateLimitMiddleware returns a Gin middleware that rate-limits requests by client IP.
func RateLimitMiddleware(limit int, interval time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(limit, interval)

	return func(c *gin.Context) {
		key := c.ClientIP()
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
}

// StrictRateLimiter returns a stricter rate limiter for auth endpoints.
func StrictRateLimiter(limit int, interval time.Duration) gin.HandlerFunc {
	return RateLimitMiddleware(limit, interval)
}
