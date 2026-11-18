package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

const acquireBuildLockLua = `
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'EX', ARGV[2]) then
  return 1
end
return 0
`

const releaseBuildLockLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

// AcquireBuildLock leases a unique owner for ten seconds. A failed command
// may have written the lock; keep its token so the caller can clean up safely.
func (r *FeedRepo) AcquireBuildLock(ctx context.Context, uid string) (string, error) {
	if err := r.buildLockReady(ctx); err != nil {
		return "", err
	}
	var owner [32]byte
	if _, err := rand.Read(owner[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(owner[:])
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, acquireBuildLockLua, []string{lockKey(uid)}, token, 10)
	if err != nil {
		return token, err
	}
	switch value {
	case int64(1):
		return token, nil
	case int64(0):
		return "", nil
	default:
		return token, errors.New("unexpected feed build lock reply")
	}
}

// ReleaseBuildLock only deletes this owner, including after its lease expired
// and another worker acquired the same key. Missing/mismatched owners are no-ops.
func (r *FeedRepo) ReleaseBuildLock(ctx context.Context, uid, token string) error {
	if err := r.buildLockReady(ctx); err != nil {
		return err
	}
	if token == "" {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err := r.rdb.EvalContextBounded(bounded, releaseBuildLockLua, []string{lockKey(uid)}, token)
	return err
}

func (r *FeedRepo) buildLockReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("feed build lock context unavailable")
	}
	return r.readReady(ctx)
}
