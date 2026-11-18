package repo

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"feedvideo.local/shared-db/cache"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type creatorStatsReader interface {
	ReadCreatorStatsPage(context.Context, string, string, int64) ([]*videomodel.VideoStats, error)
}

type CreatorRepo struct{ videoRepo videorepo.VideoRepo }

// Preserve construction; the independent Redis keys are not statistics sources.
func NewCreatorRepo(_ *cache.RedisClient, videoRepo videorepo.VideoRepo) *CreatorRepo {
	return &CreatorRepo{videoRepo: videoRepo}
}

func (r *CreatorRepo) GetFollowerCount(context.Context, string) (int64, error) {
	return 0, fmt.Errorf("creator follower source not configured")
}

// AggregateStats retains one page and discards partial totals on any failure.
// Only a terminal empty page proves completion; a short page may race changes.
func (r *CreatorRepo) AggregateStats(ctx context.Context, uid string) (plays, likes, comments, shares, collects, videoCount int64, err error) {
	actor, e := strconv.ParseInt(uid, 10, 64)
	if e != nil || actor <= 0 || strconv.FormatInt(actor, 10) != uid {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("invalid creator uid")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if r == nil {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("creator source unavailable")
	}
	source, ok := r.videoRepo.(creatorStatsReader)
	if !ok {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("creator statistics capability unavailable")
	}
	after := ""
	for {
		if e := ctx.Err(); e != nil {
			return 0, 0, 0, 0, 0, 0, e
		}
		rows, e := source.ReadCreatorStatsPage(ctx, uid, after, 500)
		if e != nil {
			return 0, 0, 0, 0, 0, 0, fmt.Errorf("read creator page: %w", e)
		}
		if e := ctx.Err(); e != nil {
			return 0, 0, 0, 0, 0, 0, e
		}
		if len(rows) > 500 {
			return 0, 0, 0, 0, 0, 0, fmt.Errorf("oversized creator statistics page")
		}
		if len(rows) == 0 {
			return plays, likes, comments, shares, collects, videoCount, nil
		}
		for _, row := range rows {
			if row == nil {
				return 0, 0, 0, 0, 0, 0, fmt.Errorf("missing creator statistics row")
			}
			id, e := primitive.ObjectIDFromHex(row.VideoID)
			if e != nil || id.IsZero() || id.Hex() != row.VideoID || row.VideoID <= after {
				return 0, 0, 0, 0, 0, 0, fmt.Errorf("invalid creator statistics continuation")
			}
			for _, pair := range []struct {
				total *int64
				value int64
			}{{&plays, row.PlayCount}, {&likes, row.LikeCount}, {&comments, row.CommentCount}, {&shares, row.ShareCount}, {&collects, row.CollectCount}, {&videoCount, 1}} {
				if pair.value < 0 || *pair.total > math.MaxInt64-pair.value {
					return 0, 0, 0, 0, 0, 0, fmt.Errorf("invalid or overflowing creator total")
				}
				*pair.total += pair.value
			}
			after = row.VideoID
		}
	}
}
