package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// A missing cache counter has no known historical base. Consumers can preserve
// an already committed primary action without treating this as a new count.
var ErrOrdinaryStatsUnavailable = errors.New("ordinary statistics base unavailable")

// Lua has no general rollback on errors. Schedule work before the counter
// mutation; a failed HINCRBY may leave extra work, never an unmarked increment.
const ordinaryStatIncrementScript = `
local statsType = redis.call('TYPE', KEYS[1]).ok
local dirtyType = redis.call('TYPE', KEYS[2]).ok
if statsType ~= 'none' and statsType ~= 'hash' then return redis.error_reply('invalid stats type') end
if dirtyType ~= 'none' and dirtyType ~= 'zset' then return redis.error_reply('invalid dirty type') end
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 then
 local ticket = redis.call('HGET', KEYS[1], 'ordinary_stats_bootstrap') or ''
 local valid = string.len(ticket) == 36
  and string.sub(ticket, 9, 9) == '-' and string.sub(ticket, 14, 14) == '-'
  and string.sub(ticket, 19, 19) == '-' and string.sub(ticket, 24, 24) == '-'
  and string.match(ticket, '^[0-9a-f]+%-[0-9a-f]+%-4[0-9a-f]+%-[89ab][0-9a-f]+%-[0-9a-f]+$')
 if not valid then return 0 end
end
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[4])
redis.call('EXPIRE', KEYS[2], 86400)
redis.call('HSET', KEYS[1], 'ordinary_stats_token', ARGV[5])
redis.call('HINCRBY', KEYS[1], ARGV[1], ARGV[2])
return 1
`

func (r *VideoRepoMongo) incrementOrdinaryStat(ctx context.Context, videoID, field string, delta int64) error {
	switch field {
	case "play_count", "like_count", "comment_count", "collect_count", "share_count":
	default:
		return fmt.Errorf("increment stat: unknown field %q", field)
	}
	value, err := r.rdb.EvalContextBounded(ctx, ordinaryStatIncrementScript,
		[]string{statsCachePrefix + ":" + videoID, dirtyStatsKey}, field, strconv.FormatInt(delta, 10), time.Now().Unix(), videoID, uuid.NewString())
	if err != nil {
		return fmt.Errorf("increment stat: %w", err)
	}
	if value == int64(0) {
		return ErrOrdinaryStatsUnavailable
	}
	if value != int64(1) {
		return fmt.Errorf("increment stat: invalid acknowledgement")
	}
	return nil
}
