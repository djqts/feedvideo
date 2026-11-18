package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"feedvideo.local/user/model"
)

type UserCacheRepo interface {
	// UserCounter
	SetUserCounter(ctx context.Context, counter *model.UserCounter) error
	GetUserCounter(ctx context.Context, userID int64) (*model.UserCounter, error)
	IncrUserCounter(ctx context.Context, userID int64, field string, delta int64) error
	// UserOnlineStatus
	SetUserOnlineStatus(ctx context.Context, status *model.UserOnlineStatus) error
	GetUserOnlineStatus(ctx context.Context, userID int64) (*model.UserOnlineStatus, error)
	// InterestCache
	SetInterestCache(ctx context.Context, interest *model.InterestCache) error
	GetInterestCache(ctx context.Context, userID int64) (*model.InterestCache, error)
}

type UserCache struct {
	rdb *redis.Client
}

func NewUserCache(rdb *redis.Client) *UserCache {
	return &UserCache{rdb: rdb}
}

// 缓存键的前缀
const (
	sessionPrefix      = "session:"
	counterPrefix      = "counter:"
	onlineStatusPrefix = "online:"
	interestPrefix     = "interest:"
)

// UserCounter
func (r *UserCache) SetUserCounter(ctx context.Context, counter *model.UserCounter) error {
	key := counterPrefix + fmt.Sprintf("%d", counter.UserID)
	data, err := json.Marshal(counter)
	if err != nil {
		return fmt.Errorf("Marshal counter: %w", err)
	}
	return r.rdb.Set(ctx, key, data, 0).Err()
}

func (r *UserCache) GetUserCounter(ctx context.Context, userID int64) (*model.UserCounter, error) {
	key := counterPrefix + fmt.Sprintf("%d", userID)
	data, err := r.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return &model.UserCounter{UserID: userID}, nil
		}
		return nil, fmt.Errorf("Get counter: %w", err)
	}
	var counter model.UserCounter
	if err := json.Unmarshal([]byte(data), &counter); err != nil {
		return nil, fmt.Errorf("Unmarshal counter: %w", err)
	}
	return &counter, nil
}

// incrCounterLua 原子增减 JSON 计数：key 不存在时以 delta 初始化；字段增减后回写。
const incrCounterLua = `
local raw = redis.call('GET', KEYS[1])
local obj
if not raw then
  obj = {}
else
  obj = cjson.decode(raw)
end
obj[ARGV[1]] = (obj[ARGV[1]] or 0) + tonumber(ARGV[2])
redis.call('SET', KEYS[1], cjson.encode(obj))
return obj[ARGV[1]]
`

// IncrUserCounter 原子调整计数（关注/粉丝/作品数），避免 Get-Modify-Set 竞态。
func (r *UserCache) IncrUserCounter(ctx context.Context, userID int64, field string, delta int64) error {
	if delta == 0 {
		return nil
	}
	key := counterPrefix + fmt.Sprintf("%d", userID)
	_, err := r.rdb.Eval(ctx, incrCounterLua, []string{key}, field, delta).Result()
	return err
}

// UserOnlineStatus
func (r *UserCache) SetUserOnlineStatus(ctx context.Context, status *model.UserOnlineStatus) error {
	key := onlineStatusPrefix + fmt.Sprintf("%d", status.UserID)
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("Marshal status: %w", err)
	}
	return r.rdb.Set(ctx, key, data, 5*time.Minute).Err()
}

func (r *UserCache) GetUserOnlineStatus(ctx context.Context, userID int64) (*model.UserOnlineStatus, error) {
	key := onlineStatusPrefix + fmt.Sprintf("%d", userID)
	data, err := r.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("Get status: %w", err)
	}
	var status model.UserOnlineStatus
	if err := json.Unmarshal([]byte(data), &status); err != nil {
		return nil, fmt.Errorf("Unmarshal status: %w", err)
	}
	return &status, nil
}

// InterestCache
func (r *UserCache) SetInterestCache(ctx context.Context, interest *model.InterestCache) error {
	key := interestPrefix + fmt.Sprintf("%d", interest.UserID)
	data, err := json.Marshal(interest)
	if err != nil {
		return fmt.Errorf("Marshal interest: %w", err)
	}
	return r.rdb.Set(ctx, key, data, 1*time.Hour).Err()
}

func (r *UserCache) GetInterestCache(ctx context.Context, userID int64) (*model.InterestCache, error) {
	key := interestPrefix + fmt.Sprintf("%d", userID)
	data, err := r.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("Get interest: %w", err)
	}
	var interest model.InterestCache
	if err := json.Unmarshal([]byte(data), &interest); err != nil {
		return nil, fmt.Errorf("Unmarshal interest: %w", err)
	}
	return &interest, nil
}
