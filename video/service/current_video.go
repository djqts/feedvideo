package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ErrVideoPendingReview marks a non-public video that still awaits moderation.
// The handler maps it to a 404 carrying a machine-readable review state so the
// web client can render “审核中” instead of a generic not-found page.
var ErrVideoPendingReview = errors.New("video pending review")

// ErrVideoRejected marks a non-public video rejected by moderation.
var ErrVideoRejected = errors.New("video rejected")

// GetVideo validates current source facts before relationship reads or public effects.
// Every operation shares the entry deadline, including a shorter caller deadline.
func (s *VideoService) GetVideo(ctx context.Context, videoID, currentUserID string) (*VideoDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id, err := primitive.ObjectIDFromHex(videoID)
	if err != nil {
		return nil, repo.ErrVideoReadInvalid
	}
	videoID = id.Hex()
	reader, ok := s.videoRepo.(repo.CurrentVideoReader)
	if !ok {
		return nil, repo.ErrVideoReadUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, detailReadUnavailable(err)
	}
	v, err := reader.GetCurrentByID(ctx, videoID)
	if err := ctx.Err(); err != nil {
		return nil, detailReadUnavailable(err)
	}
	if err != nil {
		if errors.Is(err, repo.ErrVideoNotFound) {
			return nil, repo.ErrVideoNotFound
		}
		return nil, detailReadUnavailable(err)
	}
	if v == nil || v.ID != id {
		return nil, repo.ErrVideoReadUnavailable
	}
	if v.Status == "deleted" || v.DeletedAt != nil {
		return nil, repo.ErrVideoNotFound
	}
	public := model.IsPubliclyVisible(v)
	if !public && (currentUserID == "" || v.AuthorID != currentUserID) {
		switch v.Status {
		case "pending":
			return nil, ErrVideoPendingReview
		case "rejected":
			return nil, ErrVideoRejected
		}
		return nil, repo.ErrVideoNotFound
	}

	durableLikes := false
	if marker, ok := s.likeRepo.(interface{ DurableLikesEnabled() bool }); ok {
		durableLikes = marker.DurableLikesEnabled()
	}
	stats, statsErr := s.videoRepo.GetStats(ctx, videoID)
	if err := ctx.Err(); err != nil {
		return nil, detailReadUnavailable(err)
	}
	if statsErr != nil {
		if durableLikes {
			return nil, likeStateError(statsErr)
		}
		// Preserve legacy Redis-stat degradation without recording a public play.
		stats = &model.VideoStats{VideoID: videoID}
	} else if stats == nil {
		return nil, repo.ErrVideoReadUnavailable
	}
	detail := &VideoDetail{Video: v, Stats: stats, CollectCount: stats.CollectCount, ShareCount: stats.ShareCount}
	if durableLikes {
		detail.LikeStateKnown = true
	} else {
		s.fillVideoLikes(ctx, []*VideoDetail{detail}, currentUserID)
	}
	if err := ctx.Err(); err != nil {
		return nil, detailReadUnavailable(err)
	}
	if currentUserID != "" {
		if durableLikes {
			if s.likeRepo == nil {
				return nil, repo.ErrLikeUnavailable
			}
			liked, err := s.likeRepo.IsLiked(ctx, currentUserID, videoID, "video")
			if err := ctx.Err(); err != nil {
				return nil, detailReadUnavailable(err)
			}
			if err != nil {
				return nil, likeStateError(err)
			}
			detail.Liked, detail.IsLiked = liked, liked
		}
		if s.collectRepo != nil {
			collected, err := s.collectRepo.IsCollected(ctx, currentUserID, videoID)
			if err := ctx.Err(); err != nil {
				return nil, detailReadUnavailable(err)
			}
			if err != nil {
				return nil, detailReadUnavailable(err)
			}
			detail.IsCollected = collected
		}
	}
	if public && statsErr == nil {
		_ = s.videoRepo.IncrementStat(ctx, videoID, "play_count", 1)
	}
	return detail, nil
}

func detailReadUnavailable(err error) error {
	return fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
}
