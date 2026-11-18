package service

import (
	"context"
	"fmt"
	"time"

	"feedvideo.local/video/model"
)

type statsRecoveryRepo interface {
	SyncStatsForRecovery(context.Context, string) (*model.StatsSyncReceipt, error)
}

// The public aggregate contract stays unchanged. Dirty recovery needs actual
// per-item evidence and must never infer IDs from aggregate success counts.
func (s *VideoService) BatchSyncStatsRecovery(ctx context.Context, req *SyncStatsReq) ([]model.StatsSyncOutcome, error) {
	if req == nil || len(req.VideoIDs) > 100 {
		return nil, fmt.Errorf("invalid recovery batch: maximum 100 videos")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.VideoIDs) == 0 {
		return []model.StatsSyncOutcome{}, nil
	}
	recovery, ok := s.videoRepo.(statsRecoveryRepo)
	if !ok {
		return nil, fmt.Errorf("per-item stats recovery unavailable")
	}
	rows := make([]model.StatsSyncOutcome, 0, len(req.VideoIDs))
	for i, id := range req.VideoIDs {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		receipt, err := recovery.SyncStatsForRecovery(ctx, id)
		rows = append(rows, model.StatsSyncOutcome{VideoID: id, Receipt: receipt, Err: err})
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		if i+1 < len(req.VideoIDs) {
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return rows, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return rows, nil
}
