package service

import (
	"context"
	"log/slog"

	"feedvideo.local/video/model"
)

// ListDetails adds current counters and user state without recording a play.
// Each call creates new wrappers so cached candidate lists remain user-neutral.
func (s *VideoService) ListDetails(ctx context.Context, videos []*model.Video, userID string) []*VideoDetail {
	details := s.batchDetailsForUser(ctx, videos, userID)
	if userID == "" || len(details) == 0 {
		return details
	}
	ids := make([]string, len(details))
	for i, detail := range details {
		ids[i] = detail.ID.Hex()
	}
	var collected map[string]bool
	if s.collectRepo != nil {
		var err error
		collected, err = s.collectRepo.BatchIsCollected(ctx, userID, ids)
		if err != nil {
			slog.Warn("batch collection state unavailable", "error", err)
		}
	}
	for i, detail := range details {
		detail.IsCollected = collected[ids[i]]

	}
	return details
}
