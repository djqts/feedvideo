package repo

import (
	"context"
	"log/slog"
	"strconv"
	"time"
)

type LegacyLikeTransitionWriter interface {
	ApplyLegacyLike(context.Context, string, string, string, bool) (bool, error)
}

func validLegacyLikeActor(actor string) bool {
	id, err := strconv.ParseInt(actor, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == actor
}

const legacyLikeTransitionLua = `
local kind = redis.call('TYPE', KEYS[1]).ok
if kind ~= 'none' and kind ~= 'set' then return redis.error_reply('invalid like membership type') end
if ARGV[2] == '1' then return redis.call('SADD', KEYS[1], ARGV[1]) end
return redis.call('SREM', KEYS[1], ARGV[1])
`

func (r *LikeRepoMongo) ApplyLegacyLike(ctx context.Context, actor, id, kind string, active bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !validLegacyLikeActor(actor) || !validCommentID(id) || kind != "video" && kind != "comment" {
		return false, ErrLikeInvalid
	}
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return false, ErrLikeUnavailable
	}
	desired, action := "0", "unlike"
	if active {
		desired, action = "1", "like"
	}
	if kind != "video" {
		return false, ErrLikeInvalid
	}
	snapshot, err := r.videoLikeSnapshot(ctx, id, actor, desired)
	if err != nil {
		return false, err
	}
	changed := int64(0)
	if snapshot.Changed {
		changed = 1
	}
	if changed == 0 {
		return false, nil
	}
	// Retain the legacy best-effort enqueue boundary after confirmed membership.
	if ctx.Err() == nil {
		if err := r.enqueueLike(ctx, action, actor, id, kind); err != nil {
			slog.Warn("enqueue like outbox failed", "target_id", id, "error", err)
		}
	}
	return true, nil
}
