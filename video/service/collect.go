package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func bsonForIDs(ids []string) bson.M {
	objectIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		if objectID, err := primitive.ObjectIDFromHex(id); err == nil {
			objectIDs = append(objectIDs, objectID)
		}
	}
	return bson.M{"_id": bson.M{"$in": objectIDs}}
}

type CollectService struct {
	collectRepo repo.CollectRepo
	videoRepo   repo.VideoRepo
	videoSvc    *VideoService
}

func NewCollectService(collectRepo repo.CollectRepo, videoRepo repo.VideoRepo) *CollectService {
	details := NewVideoService(videoRepo, nil)
	details.SetCollectRepo(collectRepo)
	return &CollectService{collectRepo: collectRepo, videoRepo: videoRepo, videoSvc: details}
}

// SetVideoService supplies the shared detail builder including current-user likes.
func (s *CollectService) SetVideoService(details *VideoService) {
	if details != nil {
		s.videoSvc = details
	}
}

type CollectResult struct {
	Collected    bool   `json:"collected"`
	CollectCount int64  `json:"collect_count"`
	Version      string `json:"version"`
	Changed      bool   `json:"-"`
}

var ErrCollectTargetNotFound = errors.New("collect target not found")

func ValidCollectVideoID(videoID string) bool {
	id, err := primitive.ObjectIDFromHex(videoID)
	return err == nil && id.Hex() == videoID
}

func (s *CollectService) DurableCollectionsEnabled() bool {
	_, ok := s.collectRepo.(repo.CollectStateStore)
	return ok
}

func collectServiceError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *repo.CollectVersionConflict
	if errors.As(err, &conflict) || errors.Is(err, repo.ErrCollectInvalid) || errors.Is(err, repo.ErrCollectUnavailable) || errors.Is(err, ErrCollectTargetNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", repo.ErrCollectUnavailable, err)
}

func (s *CollectService) collectResult(ctx context.Context, videoID string, state *repo.CollectState) (*CollectResult, error) {
	if state == nil {
		return nil, repo.ErrCollectUnavailable
	}
	count, err := s.collectRepo.GetCollectCount(ctx, videoID)
	if err != nil {
		return nil, collectServiceError(err)
	}
	return &CollectResult{Collected: state.Collected, CollectCount: count, Version: strconv.FormatInt(state.Version, 10), Changed: state.Changed}, nil
}

func (s *CollectService) SetCollectState(ctx context.Context, userID, videoID string, collected bool, expected *int64) (*CollectResult, error) {
	if !ValidLikeActor(userID) || !ValidCollectVideoID(videoID) || (expected != nil && *expected < 0) {
		return nil, repo.ErrCollectInvalid
	}
	store, ok := s.collectRepo.(repo.CollectStateStore)
	if !ok {
		return nil, repo.ErrCollectUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if collected {
		if s.videoRepo == nil {
			return nil, repo.ErrCollectUnavailable
		}
		video, err := s.videoRepo.GetByID(ctx, videoID)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCollectTargetNotFound
		}
		if err != nil {
			return nil, collectServiceError(err)
		}
		if !model.IsPubliclyVisible(video) || video.DeletedAt != nil {
			return nil, ErrCollectTargetNotFound
		}
	}
	state, err := store.SetCollectState(ctx, userID, videoID, collected, expected)
	if err != nil {
		return nil, collectServiceError(err)
	}
	// A count failure can follow a committed state. Return unavailable, never
	// compensate by toggling or updating a second counter source.
	return s.collectResult(ctx, videoID, state)
}

func (s *CollectService) GetCollectState(ctx context.Context, userID, videoID string) (*CollectResult, error) {
	if !ValidLikeActor(userID) || !ValidCollectVideoID(videoID) {
		return nil, repo.ErrCollectInvalid
	}
	store, ok := s.collectRepo.(repo.CollectStateStore)
	if !ok {
		return nil, repo.ErrCollectUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := store.GetCollectState(ctx, userID, videoID)
	if err != nil {
		return nil, collectServiceError(err)
	}
	return s.collectResult(ctx, videoID, state)
}

func (s *CollectService) Collect(ctx context.Context, userID, videoID string) (*CollectResult, error) {
	if store, ok := s.collectRepo.(repo.CollectStateStore); ok {
		if !ValidLikeActor(userID) || !ValidCollectVideoID(videoID) {
			return nil, repo.ErrCollectInvalid
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		state, err := store.GetCollectState(ctx, userID, videoID)
		if err != nil {
			return nil, collectServiceError(err)
		}
		if state == nil {
			return nil, repo.ErrCollectUnavailable
		}
		return s.SetCollectState(ctx, userID, videoID, !state.Collected, &state.Version)
	}
	// Legacy adapters support older in-process callers. The production Mongo
	// implementation always uses the versioned state branch above.
	if s.collectRepo == nil {
		return nil, repo.ErrCollectUnavailable
	}
	collected, count, err := s.collectRepo.Collect(ctx, userID, videoID)
	if err != nil {
		return nil, fmt.Errorf("collect: %w", err)
	}
	// 收藏计数 ±1（统一走 stats 管道，best-effort）
	delta := int64(1)
	if !collected {
		delta = -1
	}
	if err := s.videoRepo.IncrementStat(ctx, videoID, "collect_count", delta); err != nil {
		slog.Warn("incr collect_count failed", "video_id", videoID, "error", err)
	}
	return &CollectResult{Collected: collected, CollectCount: count, Version: "0", Changed: true}, nil
}

type CollectListResult struct {
	List    []*VideoDetail `json:"list"`
	Total   int64          `json:"total"`
	HasMore bool           `json:"has_more"`
}

func (s *CollectService) List(ctx context.Context, userID string, page, size int64) (*CollectListResult, error) {
	if page < 1 || size < 1 || size > 100 || page > math.MaxInt64/size {
		return nil, repo.ErrCollectInvalid
	}
	if s.collectRepo == nil {
		return nil, repo.ErrCollectUnavailable
	}
	if s.DurableCollectionsEnabled() && !ValidLikeActor(userID) {
		return nil, repo.ErrCollectInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	videoIDs, total, err := s.collectRepo.GetCollectedVideos(ctx, userID, page, size)
	if err != nil {
		return nil, collectServiceError(err)
	}
	if len(videoIDs) == 0 {
		return &CollectListResult{List: []*VideoDetail{}, Total: total, HasMore: page*size < total}, nil
	}
	if len(videoIDs) > 100 || s.videoRepo == nil {
		return nil, repo.ErrCollectUnavailable
	}
	filter := bsonForIDs(videoIDs)
	filter["status"] = "approved"
	filter["deleted_at"] = bson.M{"$exists": false}
	filter["$or"] = bson.A{bson.M{"media_status": "succeeded"}, bson.M{"media_status": ""}, bson.M{"media_status": bson.M{"$exists": false}}}
	videos, _, err := s.videoRepo.List(ctx, filter, 1, int64(len(videoIDs)))
	if err != nil {
		return nil, collectServiceError(err)
	}
	byID := make(map[string]*model.Video, len(videos))
	for _, video := range videos {
		if model.IsPubliclyVisible(video) && video.DeletedAt == nil {
			byID[video.ID.Hex()] = video
		}
	}
	ordered := make([]*model.Video, 0, len(videoIDs))
	for _, id := range videoIDs {
		if video := byID[id]; video != nil {
			ordered = append(ordered, video)
		}
	}
	details := s.videoSvc.ListDetails(ctx, ordered, userID)
	if s.DurableCollectionsEnabled() {
		if err := s.FillCollectInfo(ctx, details, userID); err != nil {
			return nil, err
		}
	}
	return &CollectListResult{List: details, Total: total, HasMore: page*size < total}, nil
}

// FillCollectInfo reads collection facts for durable callers. The common detail
// builder remains best-effort; collection lists invoke this strict pass explicitly.
func (s *CollectService) FillCollectInfo(ctx context.Context, videos []*VideoDetail, currentUserID string) error {
	if len(videos) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.collectRepo == nil {
		return repo.ErrCollectUnavailable
	}
	durable := s.DurableCollectionsEnabled()
	if durable && (len(videos) > 100 || (currentUserID != "" && !ValidLikeActor(currentUserID))) {
		return repo.ErrCollectInvalid
	}

	ids := make([]string, len(videos))
	for i, v := range videos {
		ids[i] = v.ID.Hex()
	}

	// Batch is_collected (only if logged in)
	var collected map[string]bool
	if currentUserID != "" {
		var err error
		collected, err = s.collectRepo.BatchIsCollected(ctx, currentUserID, ids)
		if durable && err != nil {
			return collectServiceError(err)
		}
	}
	var counts map[string]int64
	if durable {
		var err error
		counts, err = s.collectRepo.BatchCollectCounts(ctx, ids)
		if err != nil {
			return collectServiceError(err)
		}
	}

	for i, v := range videos {
		if collected != nil {
			v.IsCollected = collected[ids[i]]
		} else if durable {
			v.IsCollected = false
		}
		if durable {
			v.CollectCount = counts[ids[i]]
			if v.Stats == nil {
				v.Stats = &model.VideoStats{VideoID: ids[i]}
			} else {
				stats := *v.Stats
				v.Stats = &stats
			}
			v.Stats.CollectCount = v.CollectCount
		}
	}
	return nil
}
