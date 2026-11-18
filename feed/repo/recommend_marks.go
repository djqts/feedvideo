package repo

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Redis scripts do not roll back runtime errors. Validate every argument and
// the destination type before the first mutation.
const recommendMarkLua = `
if ARGV[1] == 'delete' then return redis.call('DEL', KEYS[1]) end
if ARGV[1] ~= 'mark' then return redis.error_reply('invalid feed write operation') end
local score, maximum, ttl = tonumber(ARGV[2]), tonumber(ARGV[3]), tonumber(ARGV[4])
if not score or score ~= score or score == math.huge or score == -math.huge
   or not maximum or maximum < 1 or maximum > 2000 or maximum ~= math.floor(maximum)
   or not ttl or ttl < 1 or ttl > 9223372036854 or ttl ~= math.floor(ttl)
   or #ARGV < 5 or #ARGV > 2004 then return redis.error_reply('invalid feed mark payload') end
local seen = {}
for i=5,#ARGV do
  local id = ARGV[i]
  if id == '' or string.len(id) > 128 or seen[id] then return redis.error_reply('invalid feed mark member') end
  seen[id] = true
end
local typ = redis.call('TYPE', KEYS[1]).ok
if typ ~= 'none' and typ ~= 'zset' then return redis.error_reply('invalid feed mark key type') end
for i=5,#ARGV do redis.call('ZADD', KEYS[1], ARGV[2], ARGV[i]) end
redis.call('ZREMRANGEBYRANK', KEYS[1], 0, -(maximum+1))
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1
`

func validFeedWriteUID(uid string) bool { return uid != "" && len(uid) <= 128 }

// MarkRecommended is a cache preference, not an exposure event. A failed
// receipt after dispatch may still mean the entire atomic mark was committed.
func (r *FeedRepo) MarkRecommended(ctx context.Context, uid string, ids []string) error {
	if err := r.readReady(ctx); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if !validFeedWriteUID(uid) || len(ids) > 2000 {
		return errors.New("invalid feed mark batch")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 128 || seen[id] {
			return errors.New("invalid feed mark member")
		}
		seen[id] = true
	}
	if r.dedupTTL < time.Millisecond {
		return errors.New("invalid feed dedup TTL")
	}
	maximum := r.dedupMax
	if maximum <= 0 || maximum > 2000 {
		maximum = 2000
	}
	args := make([]interface{}, 0, len(ids)+4)
	args = append(args, "mark", strconv.FormatInt(time.Now().UnixNano(), 10), maximum, r.dedupTTL.Milliseconds())
	for _, id := range ids {
		args = append(args, id)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, recommendMarkLua, []string{recKey(uid)}, args...)
	if err != nil {
		return err
	}
	if value != int64(1) {
		return errors.New("invalid feed mark receipt")
	}
	return bounded.Err()
}

func (r *FeedRepo) DelPoolMiss(ctx context.Context, uid string) error {
	if err := r.readReady(ctx); err != nil {
		return err
	}
	if !validFeedWriteUID(uid) {
		return errors.New("invalid feed miss identity")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, recommendMarkLua, []string{missKey(uid)}, "delete")
	if err != nil {
		return err
	}
	if value != int64(0) && value != int64(1) {
		return errors.New("invalid feed delete receipt")
	}
	return bounded.Err()
}
