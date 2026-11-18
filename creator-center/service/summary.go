package service

import (
	"context"
	"math"

	"feedvideo.local/creator-center/model"
)

// GetSummary calculates the estimate from the same bounded read as the cards.
func (s *CreatorService) GetSummary(ctx context.Context, uid string) (*model.SummaryResp, error) {
	stats, err := s.GetStats(ctx, uid)
	if err != nil {
		return nil, err
	}
	revenue, err := s.revenueFromStats(stats)
	if err != nil {
		return nil, err
	}
	return &model.SummaryResp{Stats: stats, Revenue: revenue}, nil
}

func (s *CreatorService) revenueFromStats(stats *model.StatsResp) (*model.RevenueResp, error) {
	revenue := float64(stats.TotalPlays) * s.cfg.CPM / 1000.0
	if math.IsNaN(s.cfg.CPM) || math.IsInf(s.cfg.CPM, 0) || math.IsNaN(revenue) || math.IsInf(revenue, 0) || revenue < 0 {
		return nil, ErrStatsUnavailable
	}
	return &model.RevenueResp{EstimatedRevenue: revenue, TotalPlays: stats.TotalPlays, CPM: s.cfg.CPM}, nil
}
