package service

import (
	"context"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"github.com/google/uuid"
	"time"
)

func validVideoLikeSnapshot(snapshot *model.VideoLikeSnapshot, id string) bool {
	if snapshot == nil || snapshot.VideoID != id || snapshot.Count < 0 || snapshot.Version < 0 || snapshot.Active && snapshot.Count == 0 {
		return false
	}
	epoch, err := uuid.Parse(snapshot.Epoch)
	return err == nil && epoch.Version() == 4 && epoch.String() == snapshot.Epoch
}
func (s *LikeService) legacyVideoLikes(ctx context.Context, id, actor string) (*model.VideoLikeSnapshot, error) {
	reader, ok := s.likeRepo.(repo.LegacyVideoLikeReader)
	if !ok {
		return nil, repo.ErrLikeUnavailable
	}
	snapshot, err := reader.ReadLegacyVideoLikes(ctx, id, actor)
	if canceled := likeContextError(ctx); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, likeStateError(err)
	}
	if !validVideoLikeSnapshot(snapshot, id) {
		return nil, repo.ErrLikeUnavailable
	}
	return snapshot, nil
}

// Optional list reads share one budget; late results never restore availability.
func (s *VideoService) fillVideoLikes(ctx context.Context, details []*VideoDetail, actor string) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	durable := false
	if marker, ok := s.likeRepo.(interface{ DurableLikesEnabled() bool }); ok {
		durable = marker.DurableLikesEnabled()
	}
	for _, d := range details {
		d.LikeStateKnown, d.IsLiked, d.Liked = false, false, false
		if ctx.Err() != nil || d.Stats == nil || s.likeRepo == nil {
			continue
		}
		id := d.ID.Hex()
		var count int64
		var active bool
		if durable {
			value, err := s.likeRepo.GetLikeCount(ctx, id, "video")
			if err != nil || value < 0 || ctx.Err() != nil {
				continue
			}
			count = value
			if actor != "" {
				value, err := s.likeRepo.IsLiked(ctx, actor, id, "video")
				if err != nil || ctx.Err() != nil {
					continue
				}
				active = value
			}
		} else {
			reader, ok := s.likeRepo.(repo.LegacyVideoLikeReader)
			if !ok {
				continue
			}
			snapshot, err := reader.ReadLegacyVideoLikes(ctx, id, actor)
			if ctx.Err() != nil || err != nil || !validVideoLikeSnapshot(snapshot, id) {
				continue
			}
			count, active = snapshot.Count, actor != "" && snapshot.Active
		}
		copied := *d.Stats
		copied.LikeCount = count
		d.Stats = &copied
		d.LikeStateKnown, d.IsLiked, d.Liked = true, active, active
	}
}
