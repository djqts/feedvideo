package repo

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	sharedconfig "feedvideo.local/shared-config"
	"feedvideo.local/shared-db/cache"
)

type TagAgg struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

const maxPrefixLen = 6 // #1 联想词最大前缀长度
const maxQueryLen = 50 // 搜索词最大长度

// normalize 归一化搜索词：TrimSpace + ToLower + 过滤空/超长。
func Normalize(q string) string {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" || utf8.RuneCountInString(q) > maxQueryLen {
		return ""
	}
	return q
}

type SearchRepo struct {
	rdb    *cache.RedisClient
	hotMax int
	sugMax int
	preMin int
	ttl    time.Duration
}

func NewSearchRepo(rdb *cache.RedisClient, cfg sharedconfig.SearchConfig) *SearchRepo {
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 300
	}
	if cfg.HotMax <= 0 {
		cfg.HotMax = 100
	}
	if cfg.SuggestMax <= 0 {
		cfg.SuggestMax = 10
	}
	if cfg.SuggestPrefixMin <= 0 {
		cfg.SuggestPrefixMin = 1
	}
	return &SearchRepo{
		rdb:    rdb,
		hotMax: cfg.HotMax,
		sugMax: cfg.SuggestMax,
		preMin: cfg.SuggestPrefixMin,
		ttl:    time.Duration(cfg.CacheTTL) * time.Second,
	}
}

func hotKey() string         { return "hot_search:" + time.Now().Format("2006-01-02") }
func sugKey(p string) string { return "search_suggest:" + p }

// IncrHotWord 异步增加热词频次（date-key ZIncrBy）。
func (r *SearchRepo) IncrHotWord(ctx context.Context, q string) error {
	_, err := r.rdb.ZIncrBy(ctx, hotKey(), q, 1)
	_ = r.rdb.Expire(ctx, hotKey(), 72*time.Hour)
	return err
}

// TopHotWords 热词 Top N。
func (r *SearchRepo) TopHotWords(ctx context.Context, n int) ([]struct {
	Word  string
	Score float64
}, error) {
	zs, err := r.rdb.ZRevRangeWithScores(ctx, hotKey(), 0, int64(n-1))
	if err != nil {
		return nil, err
	}
	out := make([]struct {
		Word  string
		Score float64
	}, 0, len(zs))
	for _, z := range zs {
		out = append(out, struct {
			Word  string
			Score float64
		}{z.Member.(string), z.Score})
	}
	return out, nil
}

// AddSuggest 拆前缀写入联想词索引，写入后 ZRemRangeByRank 限容 sugMax 条，TTL 7d。
func (r *SearchRepo) AddSuggest(ctx context.Context, q string) error {
	runes := []rune(q)
	max := len(runes)
	if max > maxPrefixLen {
		max = maxPrefixLen
	}
	for i := r.preMin; i <= max; i++ {
		prefix := string(runes[:i])
		key := sugKey(prefix)
		if err := r.rdb.ZAdd(ctx, key, 1, q); err != nil {
			return err
		}
		_ = r.rdb.ZRemRangeByRank(ctx, key, 0, -(int64(r.sugMax) + 1))
		_ = r.rdb.Expire(ctx, key, 7*24*time.Hour)
	}
	return nil
}

// GetSuggest 按 prefix 取联想词 Top N。
func (r *SearchRepo) GetSuggest(ctx context.Context, prefix string, n int) ([]struct {
	Word  string
	Score float64
}, error) {
	zs, err := r.rdb.ZRevRangeWithScores(ctx, sugKey(prefix), 0, int64(n-1))
	if err != nil {
		return nil, err
	}
	out := make([]struct {
		Word  string
		Score float64
	}, 0, len(zs))
	for _, z := range zs {
		out = append(out, struct {
			Word  string
			Score float64
		}{z.Member.(string), z.Score})
	}
	return out, nil
}

// GetCache 取缓存，未命中返回空。
func (r *SearchRepo) GetCache(ctx context.Context, key string) ([]byte, error) {
	s, err := r.rdb.Get(ctx, "search_cache:v3:"+key)
	if err != nil || s == "" {
		return nil, nil
	}
	return []byte(s), nil
}

// SetCache 写缓存（TTL 可配）。
func (r *SearchRepo) SetCache(ctx context.Context, key string, data []byte) error {
	return r.rdb.Set(ctx, "search_cache:v3:"+key, data, r.ttl)
}

// CacheKey includes schema and language identity alongside query and pagination.
func CacheKey(q string, typ string, page, size int64) string {
	raw := fmt.Sprintf("3|und|%s|%s|%d|%d", q, typ, page, size)
	return fmt.Sprintf("%x", md5.Sum([]byte(raw)))
}

// SerializeResp 序列化 SearchResp 为 JSON。
func SerializeResp(v interface{}) ([]byte, error) { return json.Marshal(v) }
