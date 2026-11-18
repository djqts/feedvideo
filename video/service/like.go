package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
)

var ErrLikeTargetNotFound = errors.New("like target not found")

func (s *LikeService) SetStateStore(store repo.LikeStateStore) {
	s.stateStore = store
	if video, ok := s.videoRepo.(*repo.VideoRepoMongo); ok {
		video.SetDurableLikes(store != nil)
	}
}

func (s *LikeService) DurableLikesEnabled() bool { return s.stateStore != nil }

// ValidLikeActor accepts only the canonical server-side positive decimal ID.
func ValidLikeActor(actor string) bool {
	id, err := strconv.ParseInt(actor, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == actor
}

func likeStateError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *repo.LikeVersionConflict
	if errors.As(err, &conflict) || errors.Is(err, repo.ErrLikeInvalid) || errors.Is(err, repo.ErrLikeBackpressure) || errors.Is(err, repo.ErrLikeUnavailable) || errors.Is(err, ErrLikeTargetNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", repo.ErrLikeUnavailable, err)
}

func (s *LikeService) SetLikeState(ctx context.Context, req *LikeReq, active bool, expected *int64) (*model.LikeState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if req == nil || !ValidLikeActor(req.UserID) || !validLikeTarget(req.TargetID, req.TargetType) || (expected != nil && *expected < 0) {
		return nil, repo.ErrLikeInvalid
	}
	if s.stateStore == nil {
		return nil, repo.ErrLikeUnavailable
	}
	recipient, err := s.likeRecipient(ctx, req.TargetID, req.TargetType)
	if err != nil {
		return nil, err
	}
	var intent *events.Event
	if active && recipient != req.UserID {
		if !ValidLikeActor(recipient) {
			return nil, repo.ErrLikeUnavailable
		}
		intent = &events.Event{Type: events.NotifLike, RecipientID: recipient, ActorID: req.UserID, TargetID: req.TargetID, TargetType: req.TargetType, CreatedAt: time.Now().Unix()}
	}
	if err := likeContextError(ctx); err != nil {
		return nil, err
	}
	state, err := s.stateStore.SetLikeState(ctx, model.LikeKey{ActorID: req.UserID, TargetID: req.TargetID, TargetType: req.TargetType}, active, expected, intent)
	if err != nil {
		return nil, likeStateError(err)
	}
	if state == nil {
		return nil, repo.ErrLikeUnavailable
	}
	return state, nil
}

func (s *LikeService) GetLikeState(ctx context.Context, userID, targetID, targetType string) (*model.LikeState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !ValidLikeActor(userID) || !validLikeTarget(targetID, targetType) {
		return nil, repo.ErrLikeInvalid
	}
	if s.stateStore == nil {
		return nil, repo.ErrLikeUnavailable
	}
	if _, err := s.likeRecipient(ctx, targetID, targetType); err != nil {
		return nil, err
	}
	state, err := s.stateStore.GetLikeState(ctx, model.LikeKey{ActorID: userID, TargetID: targetID, TargetType: targetType})
	if canceled := likeContextError(ctx); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, likeStateError(err)
	}
	if state == nil {
		return nil, repo.ErrLikeUnavailable
	}
	return state, nil
}

// Notifier 由 gateway 注入的通知发布器。video 只依赖此最小接口，不 import notification。
type Notifier interface {
	Publish(ctx context.Context, evt events.Event) error
}

type LikeService struct {
	likeRepo    repo.LikeRepo
	videoRepo   repo.VideoRepo
	commentRepo repo.CommentRepo
	notifier    Notifier
	stateStore  repo.LikeStateStore
}

func NewLikeService(likeRepo repo.LikeRepo, videoRepo repo.VideoRepo, commentRepo repo.CommentRepo, notifier Notifier) *LikeService {
	return &LikeService{
		likeRepo:    likeRepo,
		videoRepo:   videoRepo,
		commentRepo: commentRepo,
		notifier:    notifier,
	}
}

// notify 同步 + 1s 超时发布，失败仅 log，绝不影响主业务。actor==recipient 时跳过。
func (s *LikeService) notify(ctx context.Context, evt events.Event) {
	if ctx.Err() != nil || s.notifier == nil || evt.ActorID == evt.RecipientID || evt.RecipientID == "" {
		return
	}
	evt.CreatedAt = time.Now().Unix()
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.notifier.Publish(cctx, evt); err != nil {
		slog.Warn("publish notification failed", "type", evt.Type, "recipient", evt.RecipientID, "error", err)
	}
}

// ——— Like / Unlike ———

type LikeReq struct {
	UserID     string `json:"user_id"`
	TargetID   string `json:"target_id"`
	TargetType string `json:"target_type"` // "video" 或 "comment"
}

func (s *LikeService) Like(ctx context.Context, req *LikeReq) error {
	if s.stateStore != nil {
		_, err := s.SetLikeState(ctx, req, true, nil)
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if req == nil || !ValidLikeActor(req.UserID) || !validLikeTarget(req.TargetID, req.TargetType) {
		return repo.ErrLikeInvalid
	}
	recipient, err := s.likeRecipient(ctx, req.TargetID, req.TargetType)
	if err != nil {
		return err
	}
	switch req.TargetType {
	case "video":
		if s.likeRepo == nil {
			return repo.ErrLikeUnavailable
		}
		return likeStateError(s.likeVideo(ctx, req.UserID, req.TargetID, recipient))
	case "comment":
		return likeStateError(s.likeComment(ctx, req.UserID, req.TargetID))
	default:
		return repo.ErrLikeInvalid
	}
}

func (s *LikeService) Unlike(ctx context.Context, req *LikeReq) error {
	if s.stateStore != nil {
		_, err := s.SetLikeState(ctx, req, false, nil)
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if req == nil || !ValidLikeActor(req.UserID) || !validLikeTarget(req.TargetID, req.TargetType) {
		return repo.ErrLikeInvalid
	}
	if _, err := s.likeRecipient(ctx, req.TargetID, req.TargetType); err != nil {
		return err
	}

	switch req.TargetType {
	case "video":
		if s.likeRepo == nil {
			return repo.ErrLikeUnavailable
		}
		return likeStateError(s.unlikeVideo(ctx, req.UserID, req.TargetID))
	case "comment":
		return likeStateError(s.unlikeComment(ctx, req.UserID, req.TargetID))
	default:
		return repo.ErrLikeInvalid
	}
}

// ——— Query ———

func (s *LikeService) IsLiked(ctx context.Context, userID, targetID, targetType string) (bool, error) {
	if s.stateStore != nil {
		state, err := s.GetLikeState(ctx, userID, targetID, targetType)
		if err != nil {
			return false, err
		}
		return state.Active, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !ValidLikeActor(userID) || !validLikeTarget(targetID, targetType) {
		return false, repo.ErrLikeInvalid
	}
	if _, err := s.likeRecipient(ctx, targetID, targetType); err != nil {
		return false, err
	}
	if targetType == "comment" {
		snapshot, err := s.legacyCommentLikes(ctx, targetID, userID)
		if err != nil {
			return false, err
		}
		return snapshot.Active, nil
	}
	if s.likeRepo == nil {
		return false, repo.ErrLikeUnavailable
	}
	snapshot, err := s.legacyVideoLikes(ctx, targetID, userID)
	if err != nil {
		return false, err
	}
	return snapshot.Active, nil
}

func (s *LikeService) GetLikeCount(ctx context.Context, targetID, targetType string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := s.likeRecipient(ctx, targetID, targetType); err != nil {
		return 0, err
	}
	if s.stateStore != nil {
		count, err := s.stateStore.CountActive(ctx, targetID, targetType)
		if canceled := likeContextError(ctx); canceled != nil {
			return 0, canceled
		}
		return count, likeStateError(err)
	}
	if targetType == "comment" {
		snapshot, err := s.legacyCommentLikes(ctx, targetID, "")
		if err != nil {
			return 0, err
		}
		return snapshot.Count, nil
	}
	if s.likeRepo == nil {
		return 0, repo.ErrLikeUnavailable
	}
	snapshot, err := s.legacyVideoLikes(ctx, targetID, "")
	if err != nil {
		return 0, err
	}
	return snapshot.Count, nil
}

// ——— private ———

func (s *LikeService) likeVideo(ctx context.Context, userID, videoID, recipient string) error {
	if err := likeContextError(ctx); err != nil {
		return err
	}
	writer, ok := s.likeRepo.(repo.LegacyLikeTransitionWriter)
	if !ok {
		return repo.ErrLikeUnavailable
	}
	changed, err := writer.ApplyLegacyLike(ctx, userID, videoID, "video", true)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}

	// 更新视频统计
	if err := s.videoRepo.IncrementStat(ctx, videoID, "like_count", 1); err != nil {
		if errors.Is(err, repo.ErrOrdinaryStatsUnavailable) {
			slog.Warn("committed like has unknown statistics base", "video_id", videoID)
			return nil
		}
		return fmt.Errorf("increment like count: %w", err)
	}
	if ctx.Err() != nil {
		return nil
	}

	// 提升热度
	_ = s.videoRepo.AddHotScore(ctx, videoID, 2.0)

	// Reuse the current source recipient already checked before the commit.
	s.notify(ctx, events.Event{
		Type:        events.NotifLike,
		RecipientID: recipient,
		ActorID:     userID,
		TargetID:    videoID,
		TargetType:  events.TargetVideo,
	})

	return nil
}

func (s *LikeService) unlikeVideo(ctx context.Context, userID, videoID string) error {
	if err := likeContextError(ctx); err != nil {
		return err
	}
	writer, ok := s.likeRepo.(repo.LegacyLikeTransitionWriter)
	if !ok {
		return repo.ErrLikeUnavailable
	}
	changed, err := writer.ApplyLegacyLike(ctx, userID, videoID, "video", false)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := s.videoRepo.IncrementStat(ctx, videoID, "like_count", -1); err != nil {
		if errors.Is(err, repo.ErrOrdinaryStatsUnavailable) {
			slog.Warn("committed unlike has unknown statistics base", "video_id", videoID)
			return nil
		}
		return fmt.Errorf("decrement like count: %w", err)
	}
	return nil
}

func (s *LikeService) likeComment(ctx context.Context, userID, commentID string) error {
	if err := likeContextError(ctx); err != nil {
		return err
	}
	return s.commentRepo.Like(ctx, commentID, userID)
}

func (s *LikeService) unlikeComment(ctx context.Context, userID, commentID string) error {
	if err := likeContextError(ctx); err != nil {
		return err
	}
	return s.commentRepo.Unlike(ctx, commentID, userID)
}

func (s *LikeService) legacyCommentLikes(ctx context.Context, id, actor string) (*model.CommentLikeSnapshot, error) {
	reader, ok := s.commentRepo.(repo.LegacyCommentLikeReader)
	if !ok {
		return nil, repo.ErrLikeUnavailable
	}
	snapshot, err := reader.ReadLegacyLikes(ctx, id, actor)
	if canceled := likeContextError(ctx); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, likeStateError(err)
	}
	if snapshot == nil || snapshot.CommentID != id || snapshot.Count < 0 || snapshot.Version < 0 {
		return nil, repo.ErrLikeUnavailable
	}
	return snapshot, nil
}
