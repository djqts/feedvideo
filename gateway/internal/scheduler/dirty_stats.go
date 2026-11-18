package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"feedvideo.local/shared-db/cache"
	"feedvideo.local/video/model"
	videoService "feedvideo.local/video/service"
	"github.com/google/uuid"
)

type dirtyMarker struct{ VideoID, Score string }
type dirtyStore interface {
	ReadDirtyStats(context.Context, int64) ([]dirtyMarker, error)
	SettleDirtyStats(context.Context, dirtyMarker, *model.StatsSyncReceipt) (bool, error)
}
type statsRecoverySyncer interface {
	BatchSyncStatsRecovery(context.Context, *videoService.SyncStatsReq) ([]model.StatsSyncOutcome, error)
}
type redisDirtyStore struct{ rdb *cache.RedisClient }

const dirtyReadScript = `return redis.call('ZRANGEBYSCORE', KEYS[1], 0, ARGV[1], 'WITHSCORES', 'LIMIT', 0, ARGV[2])`
const dirtySettleScript = `
local dirtyType = redis.call('TYPE', KEYS[1]).ok
local statsType = redis.call('TYPE', KEYS[2]).ok
if dirtyType ~= 'none' and dirtyType ~= 'zset' then return redis.error_reply('invalid dirty type') end
if statsType ~= 'none' and statsType ~= 'hash' then return redis.error_reply('invalid stats type') end
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if ARGV[4] ~= '' and score == ARGV[4] and redis.call('HGET', KEYS[2], 'ordinary_stats_token') == ARGV[5] then
 local matches = true
 for i = 6, #ARGV, 2 do
  if redis.call('HGET', KEYS[2], ARGV[i]) ~= ARGV[i+1] then matches = false end
 end
 if matches then redis.call('ZREM', KEYS[1], ARGV[1]); return 1 end
end
-- Retry only the marker selected by this pass. A new score belongs to newer
-- work and must not be overwritten. Restoring missing work is conservative.
if not score or score == ARGV[2] then
 redis.call('ZADD', KEYS[1], ARGV[3], ARGV[1])
 redis.call('EXPIRE', KEYS[1], 86400)
end
return 0
`

func validDirtyScore(score string) bool {
	if score == "" || len(score) > 32 {
		return false
	}
	value, err := strconv.ParseFloat(score, 64)
	return err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func (s redisDirtyStore) ReadDirtyStats(ctx context.Context, limit int64) ([]dirtyMarker, error) {
	if s.rdb == nil {
		return nil, fmt.Errorf("dirty Redis unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	reply, err := s.rdb.EvalContextBounded(ctx, dirtyReadScript, []string{dirtyStatsKey}, time.Now().Unix(), limit)
	if err != nil {
		return nil, err
	}
	values, ok := reply.([]interface{})
	if !ok || len(values)%2 != 0 || len(values) > int(limit)*2 {
		return nil, fmt.Errorf("invalid dirty batch")
	}
	rows := make([]dirtyMarker, 0, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		id, idOK := values[i].(string)
		score, scoreOK := values[i+1].(string)
		if !idOK || id == "" || !scoreOK || !validDirtyScore(score) {
			return nil, fmt.Errorf("invalid dirty marker")
		}
		rows = append(rows, dirtyMarker{VideoID: id, Score: score})
	}
	return rows, nil
}

func validStatsReceipt(id string, receipt *model.StatsSyncReceipt) bool {
	if receipt == nil || receipt.VideoID != id || !receipt.Complete || !receipt.Persisted || !validDirtyScore(receipt.DirtyScore) || len(receipt.Fields) < 3 || len(receipt.Fields) > 5 {
		return false
	}
	if parsed, err := uuid.Parse(receipt.Token); err != nil || parsed.String() != receipt.Token {
		return false
	}
	for _, field := range []string{"play_count", "comment_count", "share_count"} {
		if _, ok := receipt.Fields[field]; !ok {
			return false
		}
	}
	for field, raw := range receipt.Fields {
		switch field {
		case "play_count", "like_count", "comment_count", "collect_count", "share_count":
		default:
			return false
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if len(raw) > 20 || err != nil || value < 0 {
			return false
		}
	}
	return true
}

func (s redisDirtyStore) SettleDirtyStats(ctx context.Context, marker dirtyMarker, receipt *model.StatsSyncReceipt) (bool, error) {
	if s.rdb == nil {
		return false, fmt.Errorf("dirty Redis unavailable")
	}
	if marker.VideoID == "" || !validDirtyScore(marker.Score) {
		return false, fmt.Errorf("invalid dirty marker")
	}
	args := []interface{}{marker.VideoID, marker.Score, time.Now().Unix() + 1, "", ""}
	if validStatsReceipt(marker.VideoID, receipt) {
		args[3] = receipt.DirtyScore
		args[4] = receipt.Token
		for _, field := range []string{"play_count", "like_count", "comment_count", "collect_count", "share_count"} {
			if raw, ok := receipt.Fields[field]; ok {
				args = append(args, field, raw)
			}
		}
	}
	value, err := s.rdb.EvalContextBounded(ctx, dirtySettleScript, []string{dirtyStatsKey, "video_stats:" + marker.VideoID}, args...)
	if err != nil {
		return false, err
	}
	result, ok := value.(int64)
	if !ok || result < 0 || result > 1 {
		return false, fmt.Errorf("invalid dirty settlement reply")
	}
	return result == 1, nil
}

func syncDirtyOnce(ctx context.Context, store dirtyStore, syncer StatsSyncer, limit int64) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	markers, err := store.ReadDirtyStats(ctx, limit)
	if err != nil {
		return 0, 0, err
	}
	if len(markers) == 0 {
		return 0, 0, nil
	}
	if int64(len(markers)) > limit {
		return 0, 0, fmt.Errorf("dirty batch exceeded limit")
	}
	ids := make([]string, len(markers))
	expected := map[string]bool{}
	for i, marker := range markers {
		if expected[marker.VideoID] {
			return 0, 0, fmt.Errorf("duplicate dirty marker")
		}
		expected[marker.VideoID] = true
		ids[i] = marker.VideoID
	}
	req := &videoService.SyncStatsReq{VideoIDs: ids}
	ok, failed := 0, 0
	receipts := map[string]*model.StatsSyncReceipt{}
	var batchErr error
	var errs []error
	if recovery, available := syncer.(statsRecoverySyncer); available {
		rows, callErr := recovery.BatchSyncStatsRecovery(ctx, req)
		batchErr = callErr
		seen := map[string]bool{}
		invalid := map[string]bool{}
		for _, row := range rows {
			if !expected[row.VideoID] || seen[row.VideoID] {
				invalid[row.VideoID] = true
				errs = append(errs, fmt.Errorf("unexpected/duplicate stats outcome %s", row.VideoID))
				continue
			}
			seen[row.VideoID] = true
			if row.Err != nil {
				errs = append(errs, row.Err)
			} else if validStatsReceipt(row.VideoID, row.Receipt) {
				receipts[row.VideoID] = row.Receipt
			}
		}
		for _, id := range ids {
			if !seen[id] {
				errs = append(errs, fmt.Errorf("missing stats outcome %s", id))
			}
			if invalid[id] {
				delete(receipts, id)
			}
			if batchErr == nil && receipts[id] != nil {
				ok++
			} else {
				failed++
			}
		}
	} else {
		// Aggregate counts cannot identify the persisted IDs. Keep all work, then
		// rotate only unchanged markers so the failed front cannot starve the rest.
		ok, failed, batchErr = syncer.BatchSyncStats(ctx, req)
	}
	if batchErr != nil {
		errs = append(errs, batchErr)
	}
	for _, marker := range markers {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		var receipt *model.StatsSyncReceipt
		if batchErr == nil {
			receipt = receipts[marker.VideoID]
		}
		if _, err := store.SettleDirtyStats(ctx, marker, receipt); err != nil {
			errs = append(errs, fmt.Errorf("settle stats %s: %w", marker.VideoID, err))
		}
	}
	return ok, failed, errors.Join(errs...)
}

func SyncDirtyVideoStatsOnce(ctx context.Context, rdb *cache.RedisClient, syncer StatsSyncer, limit int64) (int, int, error) {
	return syncDirtyOnce(ctx, redisDirtyStore{rdb: rdb}, syncer, limit)
}
