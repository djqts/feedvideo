package orphan

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"feedvideo.local/shared-db/cache"
)

// ObjectDeleter removes an object from object storage.
// *storage.MinioStorage satisfies it.
type ObjectDeleter interface {
	Delete(ctx context.Context, bucket, objectName string) error
}

const (
	pendingSet = "upload:pending"
	pendingTS  = "upload:pending:ts"

	// DefaultTTL is how long an uploaded-but-unpublished object is kept
	// before the reaper reclaims it as an orphan.
	DefaultTTL = 30 * time.Minute
	// DefaultInterval is how often the reaper scans for orphans.
	DefaultInterval = 15 * time.Minute
)

// Tracker records uploads that have not yet been published and reclaims
// objects that never made it into a video record.
type Tracker struct {
	rdb     *cache.RedisClient
	storage ObjectDeleter
	ttl     time.Duration
	onReap  func(int)
	// referenced 判断对象是否已被视频引用（发布成功但 Claim 失败/迟到时避免误删）。
	referenced func(ctx context.Context, bucket, object string) bool
	stop    chan struct{}
	stopOnce sync.Once
}

func NewTracker(rdb *cache.RedisClient, storage ObjectDeleter, ttl time.Duration) *Tracker {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Tracker{rdb: rdb, storage: storage, ttl: ttl, stop: make(chan struct{})}
}

// Stop 幂等停止 ReapLoop（进程优雅退出时调用）。
func (t *Tracker) Stop() {
	t.stopOnce.Do(func() { close(t.stop) })
}

// SetStorage attaches the object deleter once storage is initialized.
func (t *Tracker) SetStorage(s ObjectDeleter) {
	t.storage = s
}

// SetReapHook 注册孤儿回收成功后的回调（供指标计数，P1.2/P3.3）。
func (t *Tracker) SetReapHook(h func(int)) {
	t.onReap = h
}

// SetReferencedChecker 注入"对象是否被业务数据引用"的检查器（网关用 Mongo videos 对账）。
func (t *Tracker) SetReferencedChecker(fn func(ctx context.Context, bucket, object string) bool) {
	t.referenced = fn
}

// ParseStorageURL extracts bucket and object from a /storage/<bucket>/<object> URL.
func ParseStorageURL(url string) (bucket, object string, ok bool) {
	const prefix = "/storage/"
	if !strings.HasPrefix(url, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(url, prefix)
	idx := strings.IndexByte(rest, '/')
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// MarkPending records an uploaded object as not yet claimed by a publish.
func (t *Tracker) MarkPending(ctx context.Context, bucket, object string) error {
	member := bucket + "/" + object
	if err := t.rdb.SAdd(ctx, pendingSet, member); err != nil {
		return err
	}
	return t.rdb.HSet(ctx, pendingTS, map[string]interface{}{member: time.Now().Unix()})
}

// Claim removes an object from the pending set once a video is published.
func (t *Tracker) Claim(ctx context.Context, bucket, object string) error {
	member := bucket + "/" + object
	if err := t.rdb.SRem(ctx, pendingSet, member); err != nil {
		return err
	}
	return t.rdb.HDel(ctx, pendingTS, member)
}

// ClaimWithRetry 对认领操作做最多 3 次尝试（间隔 50ms），降低"发布成功但 Redis 抖动导致
// 对象残留 pending、随后被孤儿回收器误删"的概率。返回最终错误。
func ClaimWithRetry(ctx context.Context, claim func(context.Context, string, string) error, bucket, object string) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = claim(ctx, bucket, object); err == nil {
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

// Reap deletes objects that stayed pending longer than ttl and returns the
// number of reclaimed objects. Delete failures keep the tracking entry so
// the next cycle retries.
func (t *Tracker) Reap(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		olderThan = t.ttl
	}
	members, err := t.rdb.SMembers(ctx, pendingSet)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan).Unix()
	reclaimed := 0
	for _, member := range members {
		tsStr, err := t.rdb.HGet(ctx, pendingTS, member)
		if err != nil || tsStr == "" {
			// Missing timestamp: treat as expired and clean the entry.
			_ = t.rdb.SRem(ctx, pendingSet, member)
			_ = t.rdb.HDel(ctx, pendingTS, member)
			continue
		}
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil || ts > cutoff {
			continue
		}
		bucket, object, ok := splitMember(member)
		if !ok {
			_ = t.rdb.SRem(ctx, pendingSet, member)
			_ = t.rdb.HDel(ctx, pendingTS, member)
			continue
		}
		if t.referenced != nil && t.referenced(ctx, bucket, object) {
			// 对象已被视频引用（发布成功但 Claim 失败/迟到）→ 视为已认领：清 pending 但不删除。
			_ = t.rdb.SRem(ctx, pendingSet, member)
			_ = t.rdb.HDel(ctx, pendingTS, member)
			log.Printf("[orphan] %s referenced by a video, treat as claimed", member)
			continue
		}
		if t.storage != nil {
			if err := t.storage.Delete(ctx, bucket, object); err != nil {
				log.Printf("[orphan] delete %s failed, will retry: %v", member, err)
				continue
			}
		}
		_ = t.rdb.SRem(ctx, pendingSet, member)
		_ = t.rdb.HDel(ctx, pendingTS, member)
		reclaimed++
	}
	return reclaimed, nil
}

// ReapLoop runs Reap periodically until ctx is cancelled.
func (t *Tracker) ReapLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			n, err := t.Reap(ctx, t.ttl)
			if err != nil {
				log.Printf("[orphan] reap scan failed: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("[orphan] reclaimed %d orphaned object(s)", n)
				if t.onReap != nil {
					t.onReap(n)
				}
			}
		}
	}
}

func splitMember(member string) (bucket, object string, ok bool) {
	idx := strings.IndexByte(member, '/')
	if idx <= 0 || idx == len(member)-1 {
		return "", "", false
	}
	return member[:idx], member[idx+1:], true
}
