package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

var ErrPoolBuildRejected = errors.New("feed pool build rejected")

// PoolBuild is an opaque snapshot of the lease and pool version at build start.
// Only the repository can create a valid snapshot.
type PoolBuild struct {
	uid, token, baseVersion string
	startedAt               int64
}

func metaKey(uid string) string { return "feed:pool:meta:" + uid }

// These checks run before any destructive write. Redis scripts are atomic,
// but runtime errors do not roll back earlier commands.
const poolBuildChecksLua = `
local function keytype(key)
  return redis.call('TYPE', key).ok
end
local function version(key)
  local typ = keytype(key)
  if typ == 'none' then return '' end
  if typ ~= 'hash' then return nil end
  local v = redis.call('HMGET', key, 'pool_version', 'build_started_at', 'payload_hash')
  local started = tonumber(v[2])
  if not v[1] or v[1] == '' or not started or started <= 0 or started ~= math.floor(started)
     or not v[3] or string.len(v[3]) ~= 64 then return nil end
  return v[1]
end
local function owner(key, token)
  local typ = keytype(key)
  return typ == 'string' and redis.call('GET', key) == token
end
`

const beginPoolBuildLua = poolBuildChecksLua + `
if not owner(KEYS[1], ARGV[1]) then return {0} end
local base = version(KEYS[2])
if not base then return redis.error_reply('invalid feed pool metadata') end
local now = redis.call('TIME')
return {1, base, string.format('%.0f', tonumber(now[1])*1000 + math.floor(tonumber(now[2])/1000))}
`

// BeginPoolBuild atomically checks this owner and reads the version and Redis
// time. A failed start must not authorize any candidate-source work.
func (r *FeedRepo) BeginPoolBuild(ctx context.Context, uid, token string) (*PoolBuild, error) {
	if err := r.buildLockReady(ctx); err != nil {
		return nil, err
	}
	if uid == "" || token == "" {
		return nil, ErrPoolBuildRejected
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, beginPoolBuildLua, []string{lockKey(uid), metaKey(uid)}, token)
	if err != nil {
		return nil, err
	}
	result, ok := value.([]interface{})
	if !ok || len(result) == 0 {
		return nil, errors.New("unexpected pool build start reply")
	}
	if result[0] == int64(0) {
		return nil, ErrPoolBuildRejected
	}
	if result[0] != int64(1) || len(result) != 3 {
		return nil, errors.New("unexpected pool build start reply")
	}
	base, ok := result[1].(string)
	started, timeOK := result[2].(string)
	ms, parseErr := strconv.ParseInt(started, 10, 64)
	if !ok || !timeOK || parseErr != nil || ms <= 0 {
		return nil, errors.New("invalid pool build start reply")
	}
	return &PoolBuild{uid: uid, token: token, baseVersion: base, startedAt: ms}, nil
}

const setPoolLua = poolBuildChecksLua + `
if not owner(KEYS[1], ARGV[1]) then return 0 end
local current = version(KEYS[4])
if not current then return redis.error_reply('invalid feed pool metadata') end
local pooltype, tstype, misstyp = keytype(KEYS[2]), keytype(KEYS[3]), keytype(KEYS[5])
if (pooltype ~= 'none' and pooltype ~= 'zset') or (tstype ~= 'none' and tstype ~= 'string')
   or (misstyp ~= 'none' and misstyp ~= 'string') then return redis.error_reply('invalid feed pool key type') end
local ttl, started = tonumber(ARGV[5]), tonumber(ARGV[3])
if not ttl or ttl <= 0 or ttl ~= math.floor(ttl) or not started or started <= 0
   or started ~= math.floor(started) or #ARGV < 7 or #ARGV > 1005 or (#ARGV-5)%2 ~= 0 then
  return redis.error_reply('invalid feed pool payload')
end
local seen = {}
for i=6,#ARGV,2 do
  local score, id = tonumber(ARGV[i]), ARGV[i+1]
  if not score or score ~= score or score == math.huge or score == -math.huge
     or not id or id == '' or string.len(id) > 128 or seen[id] then
    return redis.error_reply('invalid feed pool payload')
  end
  seen[id] = true
end
if current == ARGV[1] then
  if pooltype == 'zset' and tstype == 'string'
     and redis.call('HGET', KEYS[4], 'build_started_at') == ARGV[3]
     and redis.call('HGET', KEYS[4], 'payload_hash') == ARGV[4] then return 1 end
  return 0
end
if current ~= ARGV[2] then return 0 end
redis.call('DEL', KEYS[2])
for i=6,#ARGV,2 do redis.call('ZADD', KEYS[2], ARGV[i], ARGV[i+1]) end
redis.call('SET', KEYS[3], string.format('%.0f', math.floor(started/1000)))
redis.call('DEL', KEYS[4])
redis.call('HSET', KEYS[4], 'pool_version', ARGV[1], 'build_started_at', ARGV[3], 'payload_hash', ARGV[4])
redis.call('EXPIRE', KEYS[2], ARGV[5])
redis.call('EXPIRE', KEYS[3], ARGV[5])
redis.call('EXPIRE', KEYS[4], ARGV[5])
redis.call('DEL', KEYS[5])
return 1
`

func validPoolBuild(uid string, build *PoolBuild) bool {
	return build != nil && uid != "" && build.uid == uid && build.token != "" && build.startedAt > 0
}

// canonicalPayload validates every entry before Redis sees any mutation and
// makes a replay independent of the caller's input order.
func canonicalPayload(uid string, build *PoolBuild, scored []ScoredVideo) ([]ScoredVideo, string, error) {
	if len(scored) < 1 || len(scored) > 500 {
		return nil, "", errors.New("feed pool requires 1..500 candidates")
	}
	sorted := append([]ScoredVideo(nil), scored...)
	seen := make(map[string]bool, len(sorted))
	for i, v := range sorted {
		if v.ID == "" || len(v.ID) > 128 || seen[v.ID] || math.IsNaN(v.Score) || math.IsInf(v.Score, 0) {
			return nil, "", errors.New("invalid feed pool candidate")
		}
		seen[v.ID] = true
		if v.Score == 0 {
			sorted[i].Score = 0
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	// IDs are opaque bytes, not necessarily UTF-8. JSON strings replace invalid
	// bytes with U+FFFD; byte slices use base64 and preserve distinct identities.
	type hashVideo struct {
		ID    []byte
		Score float64
	}
	hashVideos := make([]hashVideo, len(sorted))
	for i, v := range sorted {
		hashVideos[i] = hashVideo{ID: []byte(v.ID), Score: v.Score}
	}
	data, err := json.Marshal(struct {
		UID       []byte
		StartedAt int64
		Videos    []hashVideo
	}{[]byte(uid), build.startedAt, hashVideos})
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(data)
	return sorted, hex.EncodeToString(hash[:]), nil
}

// SetPool compares the owner and captured version before atomically replacing
// the pool. An exact replay acknowledges the commit without refreshing TTLs.
func (r *FeedRepo) SetPool(ctx context.Context, uid string, scored []ScoredVideo, build *PoolBuild) error {
	if err := r.buildLockReady(ctx); err != nil {
		return err
	}
	if !validPoolBuild(uid, build) {
		return ErrPoolBuildRejected
	}
	sorted, hash, err := canonicalPayload(uid, build, scored)
	if err != nil {
		return err
	}
	// Duration/config overflow must never turn an invalid TTL into a partial write.
	if r.poolTTL <= 0 || r.poolTTL > time.Duration(math.MaxInt64/2) {
		return errors.New("invalid feed pool TTL")
	}
	ttl := int64(r.poolTTL/time.Second) * 2
	if ttl <= 0 {
		return errors.New("invalid feed pool TTL")
	}
	args := make([]interface{}, 0, 5+len(sorted)*2)
	args = append(args, build.token, build.baseVersion, strconv.FormatInt(build.startedAt, 10), hash, ttl)
	for _, v := range sorted {
		args = append(args, strconv.FormatFloat(v.Score, 'g', -1, 64), v.ID)
	}
	return r.poolBuildWrite(ctx, setPoolLua, []string{lockKey(uid), poolKey(uid), tsKey(uid), metaKey(uid), missKey(uid)}, args...)
}

const setPoolMissLua = poolBuildChecksLua + `
if not owner(KEYS[1], ARGV[1]) then return 0 end
local current = version(KEYS[2])
if not current then return redis.error_reply('invalid feed pool metadata') end
if current ~= ARGV[2] or keytype(KEYS[3]) ~= 'none' then return 0 end
local typ = keytype(KEYS[4])
if typ ~= 'none' and typ ~= 'string' then return redis.error_reply('invalid feed miss key type') end
redis.call('SET', KEYS[4], '1', 'EX', 60)
return 1
`

// SetPoolMiss writes only while this snapshot still owns an absent pool.
func (r *FeedRepo) SetPoolMiss(ctx context.Context, uid string, build *PoolBuild) error {
	if err := r.buildLockReady(ctx); err != nil {
		return err
	}
	if !validPoolBuild(uid, build) {
		return ErrPoolBuildRejected
	}
	return r.poolBuildWrite(ctx, setPoolMissLua, []string{lockKey(uid), metaKey(uid), poolKey(uid), missKey(uid)}, build.token, build.baseVersion)
}

const setRetryCooldownLua = poolBuildChecksLua + `
if not owner(KEYS[1], ARGV[1]) then return 0 end
local typ = keytype(KEYS[2])
if typ ~= 'none' and typ ~= 'string' then return redis.error_reply('invalid feed retry key type') end
redis.call('SET', KEYS[2], '1', 'EX', 60)
return 1
`

func (r *FeedRepo) SetRetryCooldown(ctx context.Context, uid, token string) error {
	if err := r.buildLockReady(ctx); err != nil {
		return err
	}
	if uid == "" || token == "" {
		return ErrPoolBuildRejected
	}
	return r.poolBuildWrite(ctx, setRetryCooldownLua, []string{lockKey(uid), retryKey(uid)}, token)
}

func (r *FeedRepo) poolBuildWrite(ctx context.Context, script string, keys []string, args ...interface{}) error {
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, script, keys, args...)
	if err != nil {
		return err
	}
	switch value {
	case int64(1):
		return nil
	case int64(0):
		return ErrPoolBuildRejected
	default:
		return fmt.Errorf("unexpected pool build write reply: %v", value)
	}
}
