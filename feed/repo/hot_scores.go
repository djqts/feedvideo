package repo

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"
)

// One read samples normalization and candidate heat from the same sorted set.
// No cache writes and no Redis-version-specific ZMSCORE dependency are needed.
const batchHotScoresLua = `
local top = redis.call('ZREVRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local result = {top[2] or '0'}
for i, id in ipairs(ARGV) do
  result[i + 1] = redis.call('ZSCORE', KEYS[1], id) or '0'
end
return result
`

func (r *FeedRepo) BatchHotScores(ctx context.Context, ids []string) (map[string]float64, float64, error) {
	if ctx == nil {
		return nil, 0, errors.New("feed heat context unavailable")
	}
	if err := r.readReady(ctx); err != nil {
		return nil, 0, err
	}
	if len(ids) > 500 {
		return nil, 0, errors.New("feed heat candidate count exceeds 500")
	}
	args := make([]interface{}, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for i, id := range ids {
		if id == "" || len(id) > 128 {
			return nil, 0, errors.New("invalid feed heat candidate")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, 0, errors.New("duplicate feed heat candidate")
		}
		seen[id] = struct{}{}
		args[i] = id
	}
	if len(ids) == 0 {
		return map[string]float64{}, 0, nil
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, batchHotScoresLua, []string{hotVideosKey}, args...)
	if err != nil {
		return nil, 0, err
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != len(ids)+1 {
		return nil, 0, errors.New("invalid feed heat reply")
	}
	parsed := make([]float64, len(values))
	for i, raw := range values {
		text, ok := raw.(string)
		if !ok {
			return nil, 0, errors.New("invalid feed heat value type")
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, 0, errors.New("invalid feed heat number")
		}
		parsed[i] = number
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	scores := make(map[string]float64, len(ids))
	for i, id := range ids {
		scores[id] = parsed[i+1]
	}
	return scores, parsed[0], nil
}
