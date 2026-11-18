package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RelationCacheRepo 关系缓存接口
type RelationCacheRepo interface {
	AddFollowing(ctx context.Context, userID, targetID int64) error
	RemoveFollowing(ctx context.Context, userID, targetID int64) error
	AddFollower(ctx context.Context, userID, fanID int64) error
	RemoveFollower(ctx context.Context, userID, fanID int64) error
	AddBlack(ctx context.Context, userID, targetID int64) error
	RemoveBlack(ctx context.Context, userID, targetID int64) error
	AddFriend(ctx context.Context, userID, friendID int64) error
	RemoveFriend(ctx context.Context, userID, friendID int64) error
	IsFollowing(ctx context.Context, userID, targetID int64) (bool, error)
	IsFollower(ctx context.Context, userID, fanID int64) (bool, error)
	IsBlacked(ctx context.Context, userID, targetID int64) (bool, error)
	IsFriend(ctx context.Context, userID, friendID int64) (bool, error)
	GetFollowingIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetFollowerIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetBlackIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetFriendIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetCommonFollowing(ctx context.Context, userA, userB int64) ([]int64, error)
	SetFollowingIDs(ctx context.Context, userID int64, ids []int64) error
	SetFollowerIDs(ctx context.Context, userID int64, ids []int64) error
	SetBlackIDs(ctx context.Context, userID int64, ids []int64) error
	SetFriendIDs(ctx context.Context, userID int64, ids []int64) error
}

// 分区配置
const (
	partitions      = 1024               // 固定分区数
	cacheTTL        = 7 * 24 * time.Hour // 分区缓存有效期
	maxFieldsPerKey = 10000              // 每个分区最多存储的用户数（容灾）
)

// 关系类型对应的 key 前缀
const (
	prefixFollowing = "fol"
	prefixFollowers = "fer"
	prefixFriends   = "frd"
	prefixBlack     = "blk"
)

// Lua 脚本：原子性向缓存列表头部添加 ID
var addIDLua = redis.NewScript(`
	local key = KEYS[1]
	local field = ARGV[1]
	local targetID = tonumber(ARGV[2])
	local ttl = tonumber(ARGV[3])

	local raw = redis.call('HGET', key, field)
	if raw == 'null' then
		redis.call('HDEL', key, field)
		return 0
	end
	local ids = {}
	if raw then
		ids = cjson.decode(raw)
	end

	local newIds = {targetID}
	for i, id in ipairs(ids) do
		if id ~= targetID then
			table.insert(newIds, id)
		end
	end

	redis.call('HSET', key, field, cjson.encode(newIds))
	redis.call('EXPIRE', key, ttl)
	return 1
`)

// Lua 脚本：原子性从缓存列表删除指定 ID
var removeIDLua = redis.NewScript(`
	local key = KEYS[1]
	local field = ARGV[1]
	local targetID = tonumber(ARGV[2])
	local ttl = tonumber(ARGV[3])

	local raw = redis.call('HGET', key, field)
	if not raw then
		return 0
	end
	if raw == 'null' then
		redis.call('HDEL', key, field)
		return 0
	end

	local ids = cjson.decode(raw)
	local newIds = {}
	for i, id in ipairs(ids) do
		if id ~= targetID then
			table.insert(newIds, id)
		end
	end

	redis.call('HSET', key, field, cjson.encode(newIds))
	redis.call('EXPIRE', key, ttl)
	return 1
`)

type relationCacheRepo struct {
	rdb *redis.Client
}

func NewRelationCacheRepo(rdb *redis.Client) RelationCacheRepo {
	return &relationCacheRepo{rdb: rdb}
}

// ---------- 基础操作：读写分区 Hash ----------

// partitionKey 生成分区 key
func partitionKey(prefix string, userID int64) string {
	p := userID % partitions
	return fmt.Sprintf("rel:%s:%d", prefix, p)
}

// getIDsFromCache 从缓存中读取某个用户的 ID 列表（已按时间倒序）
func (r *relationCacheRepo) getIDsFromCache(ctx context.Context, prefix string, userID int64, offset, limit int64) ([]int64, error) {
	key := partitionKey(prefix, userID)
	field := strconv.FormatInt(userID, 10)

	// 读取 Hash field
	raw, err := r.rdb.HGet(ctx, key, field).Result()
	if err == redis.Nil {
		return nil, nil // 未命中
	}
	if err != nil {
		return nil, err
	}

	// 解析 JSON
	var items []int64
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		// Lua cjson encodes an empty table as "{}" (object), not "[]" (array).
		// Normalize empty-object => empty slice so callers get a consistent type.
		if raw == "{}" {
			return []int64{}, nil
		}
		return nil, fmt.Errorf("cache unmarshal error: %w", err)
	}
	// Legacy nil slices were cached as "null". Writes against those entries
	// failed in Lua, so the empty value may be stale and must be reloaded.
	if items == nil {
		return nil, nil
	}

	// 分页（若未指定 offset/limit，返回全部）
	if offset >= int64(len(items)) {
		return []int64{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(items)) {
		end = int64(len(items))
	}
	return items[offset:end], nil
}

// setIDsToCache 将 ID 列表写入缓存
func (r *relationCacheRepo) setIDsToCache(ctx context.Context, prefix string, userID int64, ids []int64) error {
	key := partitionKey(prefix, userID)
	field := strconv.FormatInt(userID, 10)
	if ids == nil {
		ids = []int64{}
	}

	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}

	pipe := r.rdb.Pipeline()
	pipe.HSet(ctx, key, field, data)
	pipe.Expire(ctx, key, cacheTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// removeIDFromCache 从列表中移除指定 ID（原子操作）
func (r *relationCacheRepo) removeIDFromCache(ctx context.Context, prefix string, userID, targetID int64) error {
	key := partitionKey(prefix, userID)
	field := strconv.FormatInt(userID, 10)
	ttlSeconds := int64(cacheTTL.Seconds())
	return removeIDLua.Run(ctx, r.rdb, []string{key}, field, targetID, ttlSeconds).Err()
}

// addIDToCache 向列表头部添加一个 ID（原子操作，最新关注在前）
func (r *relationCacheRepo) addIDToCache(ctx context.Context, prefix string, userID, targetID int64) error {
	key := partitionKey(prefix, userID)
	field := strconv.FormatInt(userID, 10)
	ttlSeconds := int64(cacheTTL.Seconds())
	return addIDLua.Run(ctx, r.rdb, []string{key}, field, targetID, ttlSeconds).Err()
}

// ---------- 公共方法实现 ----------

func (r *relationCacheRepo) AddFollowing(ctx context.Context, userID, targetID int64) error {
	return r.addIDToCache(ctx, prefixFollowing, userID, targetID)
}

func (r *relationCacheRepo) RemoveFollowing(ctx context.Context, userID, targetID int64) error {
	return r.removeIDFromCache(ctx, prefixFollowing, userID, targetID)
}

func (r *relationCacheRepo) AddFollower(ctx context.Context, userID, fanID int64) error {
	return r.addIDToCache(ctx, prefixFollowers, userID, fanID)
}

func (r *relationCacheRepo) RemoveFollower(ctx context.Context, userID, fanID int64) error {
	return r.removeIDFromCache(ctx, prefixFollowers, userID, fanID)
}

func (r *relationCacheRepo) AddBlack(ctx context.Context, userID, targetID int64) error {
	return r.addIDToCache(ctx, prefixBlack, userID, targetID)
}

func (r *relationCacheRepo) RemoveBlack(ctx context.Context, userID, targetID int64) error {
	return r.removeIDFromCache(ctx, prefixBlack, userID, targetID)
}

func (r *relationCacheRepo) AddFriend(ctx context.Context, userID, friendID int64) error {
	return r.addIDToCache(ctx, prefixFriends, userID, friendID)
}

func (r *relationCacheRepo) RemoveFriend(ctx context.Context, userID, friendID int64) error {
	return r.removeIDFromCache(ctx, prefixFriends, userID, friendID)
}

// ---------- 全量回填缓存 ----------

func (r *relationCacheRepo) SetFollowingIDs(ctx context.Context, userID int64, ids []int64) error {
	return r.setIDsToCache(ctx, prefixFollowing, userID, ids)
}

func (r *relationCacheRepo) SetFollowerIDs(ctx context.Context, userID int64, ids []int64) error {
	return r.setIDsToCache(ctx, prefixFollowers, userID, ids)
}

func (r *relationCacheRepo) SetBlackIDs(ctx context.Context, userID int64, ids []int64) error {
	return r.setIDsToCache(ctx, prefixBlack, userID, ids)
}

func (r *relationCacheRepo) SetFriendIDs(ctx context.Context, userID int64, ids []int64) error {
	return r.setIDsToCache(ctx, prefixFriends, userID, ids)
}

// ---------- 关系存在性判断 ----------

func (r *relationCacheRepo) isMember(ctx context.Context, prefix string, userID, targetID int64) (bool, error) {
	ids, err := r.getIDsFromCache(ctx, prefix, userID, 0, 0) // 获取全部
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == targetID {
			return true, nil
		}
	}
	return false, nil
}

func (r *relationCacheRepo) IsFollowing(ctx context.Context, userID, targetID int64) (bool, error) {
	return r.isMember(ctx, prefixFollowing, userID, targetID)
}

func (r *relationCacheRepo) IsFollower(ctx context.Context, userID, fanID int64) (bool, error) {
	return r.isMember(ctx, prefixFollowers, userID, fanID)
}

func (r *relationCacheRepo) IsBlacked(ctx context.Context, userID, targetID int64) (bool, error) {
	return r.isMember(ctx, prefixBlack, userID, targetID)
}

func (r *relationCacheRepo) IsFriend(ctx context.Context, userID, friendID int64) (bool, error) {
	return r.isMember(ctx, prefixFriends, userID, friendID)
}

// ---------- 分页列表 ----------

func (r *relationCacheRepo) GetFollowingIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	return r.getIDsFromCache(ctx, prefixFollowing, userID, offset, limit)
}

func (r *relationCacheRepo) GetFollowerIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	return r.getIDsFromCache(ctx, prefixFollowers, userID, offset, limit)
}

func (r *relationCacheRepo) GetBlackIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	return r.getIDsFromCache(ctx, prefixBlack, userID, offset, limit)
}

func (r *relationCacheRepo) GetFriendIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	return r.getIDsFromCache(ctx, prefixFriends, userID, offset, limit)
}

// ---------- 共同关注 ----------

func (r *relationCacheRepo) GetCommonFollowing(ctx context.Context, userA, userB int64) ([]int64, error) {
	idsA, err := r.getIDsFromCache(ctx, prefixFollowing, userA, 0, 0)
	if err != nil {
		return nil, err
	}
	idsB, err := r.getIDsFromCache(ctx, prefixFollowing, userB, 0, 0)
	if err != nil {
		return nil, err
	}

	set := make(map[int64]struct{}, len(idsA))
	for _, id := range idsA {
		set[id] = struct{}{}
	}

	common := make([]int64, 0)
	for _, id := range idsB {
		if _, ok := set[id]; ok {
			common = append(common, id)
		}
	}
	return common, nil
}
