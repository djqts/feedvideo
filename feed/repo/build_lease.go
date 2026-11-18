package repo

import (
	"context"
	"errors"
	"time"
)

const renewPoolBuildLua = `
if ARGV[2] ~= 'renew' or ARGV[3] ~= '10000' then return redis.error_reply('invalid feed renewal payload') end
if redis.call('TYPE', KEYS[1]).ok ~= 'string'
   or redis.call('GET', KEYS[1]) ~= ARGV[1]
   or redis.call('PTTL', KEYS[1]) <= 0 then return 0 end
return redis.call('PEXPIRE', KEYS[1], ARGV[3])
`

// RenewPoolBuild extends only a currently leased owner. A failed receipt is
// unknown; callers cancel work rather than assuming the lease is still valid.
func (r *FeedRepo) RenewPoolBuild(ctx context.Context, uid string, build *PoolBuild) error {
	if err := r.buildLockReady(ctx); err != nil {
		return err
	}
	if !validPoolBuild(uid, build) {
		return ErrPoolBuildRejected
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, renewPoolBuildLua, []string{lockKey(uid)}, build.token, "renew", 10000)
	if err != nil {
		return err
	}
	switch value {
	case int64(1):
		return bounded.Err()
	case int64(0):
		return ErrPoolBuildRejected
	default:
		return errors.New("invalid feed lease renewal receipt")
	}
}
