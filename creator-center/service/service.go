package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedvideo.local/creator-center/model"
	sharedconfig "feedvideo.local/shared-config"
	usermodel "feedvideo.local/user/model"
)

var ErrStatsUnavailable = errors.New("creator statistics unavailable")

// creatorRepo is the minimal repository dependency of CreatorService.
// *repo.CreatorRepo satisfies it.
type creatorRepo interface {
	GetFollowerCount(ctx context.Context, uid string) (int64, error)
	AggregateStats(ctx context.Context, uid string) (plays, likes, comments, shares, collects, videoCount int64, err error)
}

// profileUpdater updates a user profile document (Mongo). *userrepo.UserProfile satisfies it.
type profileUpdater interface {
	UpdateProfile(ctx context.Context, userID int64, fields map[string]interface{}) error
}

type CreatorService struct {
	repo     creatorRepo
	userRepo profileUpdater
	cfg      sharedconfig.CreatorConfig
	adminIDs []string
	// followerCountProvider reads current persisted relations. Production rejects
	// an unconfigured source instead of treating a missing cache key as zero.
	followerCountProvider    func(ctx context.Context, uid string) (int64, error)
	verificationTargetReader func(context.Context, int64) (*usermodel.User, error)
}

func NewCreatorService(repo creatorRepo, userRepo profileUpdater, cfg sharedconfig.CreatorConfig, adminIDs []string) *CreatorService {
	if cfg.CPM <= 0 {
		cfg.CPM = 2.0
	}
	return &CreatorService{repo: repo, userRepo: userRepo, cfg: cfg, adminIDs: adminIDs}
}

// SetFollowerCountProvider injects the authoritative current follower count.
func (s *CreatorService) SetFollowerCountProvider(fn func(ctx context.Context, uid string) (int64, error)) {
	s.followerCountProvider = fn
}

func (s *CreatorService) GetStats(ctx context.Context, uid string) (*model.StatsResp, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatsUnavailable, err)
	}
	var followers int64
	var err error
	if s.followerCountProvider != nil {
		followers, err = s.followerCountProvider(ctx, uid)
	} else {
		followers, err = s.repo.GetFollowerCount(ctx, uid)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: followers: %w", ErrStatsUnavailable, err)
	}
	if followers < 0 {
		return nil, ErrStatsUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatsUnavailable, err)
	}
	plays, likes, comments, shares, collects, videoCount, err := s.repo.AggregateStats(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("%w: aggregate stats: %w", ErrStatsUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatsUnavailable, err)
	}
	for _, value := range []int64{plays, likes, comments, shares, collects, videoCount} {
		if value < 0 {
			return nil, ErrStatsUnavailable
		}
	}
	return &model.StatsResp{
		Scope: "own_non_deleted", PlayBasis: "legacy_detail_views",
		FollowerCount: followers,
		TotalPlays:    plays, TotalLikes: likes, TotalComments: comments, TotalShares: shares, TotalCollects: collects,
		VideoCount: videoCount,
	}, nil
}

func (s *CreatorService) GetRevenue(ctx context.Context, uid string) (*model.RevenueResp, error) {
	stats, err := s.GetStats(ctx, uid)
	if err != nil {
		return nil, err
	}
	return s.revenueFromStats(stats)
}

func (s *CreatorService) IsAdmin(uid string) bool {
	for _, id := range s.adminIDs {
		if id == uid {
			return true
		}
	}
	return false
}
