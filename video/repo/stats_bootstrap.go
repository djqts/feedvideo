package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var errStatsBootstrapUnavailable = errors.New("ordinary statistics bootstrap unavailable")

// Keep the ticket in the counter hash: eviction removes both together. A
// separate ticket key could outlive lost counts and wrongly authorize zeros.
const prepareOrdinaryStatsBootstrapScript = `
local kind = redis.call('TYPE', KEYS[1]).ok
if kind == 'none' then
 redis.call('HSET', KEYS[1], 'ordinary_stats_bootstrap', ARGV[1])
 return ARGV[1]
end
if kind ~= 'hash' then return redis.error_reply('invalid bootstrap hash type') end
local ticket = redis.call('HGET', KEYS[1], 'ordinary_stats_bootstrap') or ''
if string.len(ticket) ~= 36 then return '' end
return ticket
`

const completeOrdinaryStatsBootstrapScript = `
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' then return 0 end
if redis.call('HGET', KEYS[1], 'ordinary_stats_bootstrap') ~= ARGV[1] then return 0 end
for i = 2, #ARGV do redis.call('HSETNX', KEYS[1], ARGV[i], '0') end
redis.call('HDEL', KEYS[1], 'ordinary_stats_bootstrap')
return 1
`

func validStatsBootstrapID(id string) bool {
	value, err := primitive.ObjectIDFromHex(id)
	return err == nil && !value.IsZero() && value.Hex() == id
}

func validStatsBootstrapTicket(ticket string) bool {
	value, err := uuid.Parse(ticket)
	return err == nil && value.Version() == 4 && value.String() == ticket
}

func (r *VideoRepoMongo) prepareOrdinaryStatsBootstrap(ctx context.Context, id string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validStatsBootstrapID(id) {
		return "", errStatsBootstrapUnavailable
	}
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return "", nil
	}
	value, err := r.rdb.EvalContextBounded(ctx, prepareOrdinaryStatsBootstrapScript,
		[]string{statsCachePrefix + ":" + id}, uuid.NewString())
	if err != nil {
		return "", err
	}
	ticket, ok := value.(string)
	if !ok || ticket != "" && !validStatsBootstrapTicket(ticket) {
		return "", errStatsBootstrapUnavailable
	}
	return ticket, nil
}

// Call only after an acknowledged new Mongo insert, never on readback/replay.
func (r *VideoRepoMongo) completeOrdinaryStatsBootstrap(ctx context.Context, id, ticket string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validStatsBootstrapID(id) || !validStatsBootstrapTicket(ticket) || r == nil || r.rdb == nil || r.rdb.Client == nil {
		return errStatsBootstrapUnavailable
	}
	args := []interface{}{ticket}
	for _, field := range r.ordinaryWriteFields() {
		args = append(args, field)
	}
	value, err := r.rdb.EvalContextBounded(ctx, completeOrdinaryStatsBootstrapScript,
		[]string{statsCachePrefix + ":" + id}, args...)
	if err != nil {
		return err
	}
	if value != int64(1) {
		return errStatsBootstrapUnavailable
	}
	return nil
}
