package repo

import (
	"context"
	"errors"
	"time"
)

// One bounded ranking sample; exclusion stays local and adds no script work.
const hotFillLua = `return redis.call('ZREVRANGE', KEYS[1], 0, ARGV[1])`

// HotFill preserves ranking order and excludes already picked IDs. The raw
// window is bounded independently of exclusions, and validated in full before
// it can authorize any output.
func (r *FeedRepo) HotFill(ctx context.Context, n int64, exclude map[string]bool) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("feed fill context unavailable")
	}
	if err := r.readReady(ctx); err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}
	if n > 500 {
		n = 500
	}
	recall := r.recallSize
	if recall <= 0 {
		recall = 200
	}
	if recall > 500 {
		recall = 500
	}
	scanCap := recall * 4
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := r.rdb.EvalContextBounded(bounded, hotFillLua, []string{hotVideosKey}, scanCap-1)
	if err != nil {
		return nil, err
	}
	window, ok := value.([]interface{})
	if !ok || int64(len(window)) > scanCap {
		return nil, errors.New("invalid feed fill reply")
	}
	ids := make([]string, len(window))
	seen := make(map[string]bool, len(window))
	for i, raw := range window {
		if err := bounded.Err(); err != nil {
			return nil, err
		}
		id, ok := raw.(string)
		if !ok || id == "" || len(id) > 128 || seen[id] {
			return nil, errors.New("invalid feed fill candidate")
		}
		ids[i], seen[id] = id, true
	}
	var out []string
	for _, id := range ids {
		if exclude[id] {
			continue
		}
		out = append(out, id)
		if int64(len(out)) == n {
			break
		}
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
