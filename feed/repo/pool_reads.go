package repo

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Operation and keys are internal, never supplied directly by an HTTP caller.
// State samples existence and timestamp atomically without mutating the pool.
const poolReadLua = `
if ARGV[1] == 'state' then
  if redis.call('EXISTS', KEYS[1]) == 0 then return {0, ''} end
  return {1, redis.call('GET', KEYS[2]) or ''}
elseif ARGV[1] == 'window' then
  return redis.call('ZREVRANGE', KEYS[1], 0, ARGV[2])
elseif ARGV[1] == 'card' then
  return redis.call('ZCARD', KEYS[1])
elseif ARGV[1] == 'exists' then
  return redis.call('EXISTS', KEYS[1])
end
return redis.error_reply('invalid feed read operation')
`

func (r *FeedRepo) readPool(ctx context.Context, op string, keys []string, args ...interface{}) (interface{}, error) {
	if err := r.readReady(ctx); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return r.rdb.EvalContextBounded(bounded, poolReadLua, keys, append([]interface{}{op}, args...)...)
}

func (r *FeedRepo) PoolState(ctx context.Context, uid string) (PoolState, error) {
	value, err := r.readPool(ctx, "state", []string{poolKey(uid), tsKey(uid)})
	if err != nil {
		return PoolMissing, err
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != 2 {
		return PoolMissing, errors.New("invalid feed state reply")
	}
	exists, ok := values[0].(int64)
	tsText, textOK := values[1].(string)
	if !ok || !textOK || (exists != 0 && exists != 1) || (exists == 0 && tsText != "") {
		return PoolMissing, errors.New("invalid feed state value")
	}
	if err := ctx.Err(); err != nil {
		return PoolMissing, err
	}
	if exists == 0 {
		return PoolMissing, nil
	}
	ts, err := strconv.ParseInt(tsText, 10, 64)
	if err != nil || time.Now().Unix()-ts > int64(r.poolTTL/time.Second) {
		return PoolStale, nil
	}
	return PoolFresh, nil
}

func (r *FeedRepo) PoolWindow(ctx context.Context, uid string, stop int64) ([]string, error) {
	if err := r.readReady(ctx); err != nil {
		return nil, err
	}
	if stop < 0 {
		return nil, errors.New("invalid pool stop")
	}
	if stop > 499 {
		stop = 499
	}
	return r.poolMembers(ctx, poolKey(uid), stop)
}

func (r *FeedRepo) PoolCard(ctx context.Context, uid string) (int64, error) {
	value, err := r.readPool(ctx, "card", []string{poolKey(uid)})
	if err != nil {
		return 0, err
	}
	card, ok := value.(int64)
	if !ok || card < 0 {
		return 0, errors.New("invalid feed cardinality reply")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return card, nil
}

func (r *FeedRepo) InRetryCooldown(ctx context.Context, uid string) (bool, error) {
	return r.poolExists(ctx, retryKey(uid))
}

func (r *FeedRepo) IsPoolMiss(ctx context.Context, uid string) (bool, error) {
	return r.poolExists(ctx, missKey(uid))
}

func (r *FeedRepo) poolExists(ctx context.Context, key string) (bool, error) {
	value, err := r.readPool(ctx, "exists", []string{key})
	if err != nil {
		return false, err
	}
	exists, ok := value.(int64)
	if !ok || (exists != 0 && exists != 1) {
		return false, errors.New("invalid feed existence reply")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (r *FeedRepo) GetRecommended(ctx context.Context, uid string) (map[string]bool, error) {
	members, err := r.poolMembers(ctx, recKey(uid), 1999)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		seen[member] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return seen, nil
}

func (r *FeedRepo) poolMembers(ctx context.Context, key string, stop int64) ([]string, error) {
	value, err := r.readPool(ctx, "window", []string{key}, stop)
	if err != nil {
		return nil, err
	}
	values, ok := value.([]interface{})
	if !ok || int64(len(values)) > stop+1 {
		return nil, errors.New("invalid feed window reply")
	}
	members := make([]string, len(values))
	seen := make(map[string]bool, len(values))
	for i, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		member, ok := value.(string)
		if !ok || member == "" || len(member) > 128 || seen[member] {
			return nil, errors.New("invalid feed window member")
		}
		members[i], seen[member] = member, true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return members, nil
}
