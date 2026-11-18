package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	events "feedvideo.local/shared-events"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
)

type CommentService struct {
	commentRepo repo.CommentRepo
	videoRepo   repo.VideoRepo
	notifier    Notifier
	sensitive   SensitiveChecker
	likeService *LikeService
}

func (s *CommentService) SetLikeService(like *LikeService) { s.likeService = like }
func (s *CommentService) DurableLikesEnabled() bool {
	return s.likeService != nil && s.likeService.DurableLikesEnabled()
}

func NewCommentService(commentRepo repo.CommentRepo, videoRepo repo.VideoRepo, notifier Notifier) *CommentService {
	return &CommentService{
		commentRepo: commentRepo,
		videoRepo:   videoRepo,
		notifier:    notifier,
	}
}

// SetSensitiveChecker 注入内容敏感词检查器（可选）。
func (s *CommentService) SetSensitiveChecker(c SensitiveChecker) {
	s.sensitive = c
}

func (s *CommentService) notify(ctx context.Context, evt events.Event) {
	if s.notifier == nil || evt.ActorID == evt.RecipientID || evt.RecipientID == "" {
		return
	}
	evt.CreatedAt = time.Now().Unix()
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.notifier.Publish(cctx, evt); err != nil {
		slog.Warn("publish notification failed", "type", evt.Type, "recipient", evt.RecipientID, "error", err)
	}
}

// ——— Post ———

type PostCommentReq struct {
	VideoID  string `json:"video_id"`
	UserID   string `json:"user_id"`
	Content  string `json:"content"`
	ParentID string `json:"parent_id"`
}

type PostCommentResp struct {
	CommentID string `json:"comment_id"`
}

// htmlTagRe 移除以防存储型 XSS：评论仅支持纯文本
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// sanitizeContent 净化评论内容：去 HTML 标签、Trim、限长 500 字符
func sanitizeContent(content string) string {
	content = htmlTagRe.ReplaceAllString(content, "")
	content = strings.TrimSpace(content)
	if r := utf8.RuneCountInString(content); r > 500 {
		runes := []rune(content)
		content = string(runes[:500])
	}
	return content
}

func (s *CommentService) PostComment(ctx context.Context, req *PostCommentReq) (*PostCommentResp, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if req == nil || !ValidLikeActor(req.UserID) || !validCommentTarget(req.VideoID) || req.ParentID != "" && !validCommentTarget(req.ParentID) {
		return nil, repo.ErrCommentInvalid
	}
	req.Content = sanitizeContent(req.Content)
	if req.Content == "" {
		return nil, fmt.Errorf("%w: content is required", repo.ErrCommentInvalid)
	}
	video, err := s.currentCommentVideo(ctx, req.VideoID)
	if err != nil {
		return nil, err
	}
	if s.commentRepo == nil {
		return nil, repo.ErrCommentUnavailable
	}
	if s.sensitive != nil {
		hit, err := s.sensitive.ContainsSensitive(ctx, req.Content)
		if err == nil && hit {
			return nil, fmt.Errorf("%w: content contains sensitive words", ErrBadRequest)
		}
	}

	comment := &model.Comment{
		VideoID: req.VideoID,
		UserID:  req.UserID,
		Content: req.Content,
	}

	if req.ParentID != "" {
		parent, err := s.commentRepo.GetByID(ctx, req.ParentID)
		if err != nil {
			return nil, commentSourceError(err)
		}
		if parent == nil || parent.ID.Hex() != req.ParentID {
			return nil, repo.ErrCommentUnavailable
		}
		if parent.VideoID != req.VideoID || parent.ParentID != "" || !model.IsVisibleComment(parent) {
			return nil, repo.ErrCommentNotFound
		}
		if err := ctx.Err(); err != nil {
			return nil, commentSourceError(err)
		}
		if err := s.commentRepo.AddReply(ctx, req.ParentID, comment); err != nil {
			return nil, commentSourceError(err)
		}
		s.notifyCommentTarget(ctx, req, video)
		s.notify(ctx, events.Event{
			Type:        events.NotifComment,
			RecipientID: parent.UserID,
			ActorID:     req.UserID,
			TargetID:    req.ParentID,
			TargetType:  events.TargetComment,
			Content:     req.Content,
		})
		// 评论计数 +1（回复计入视频评论数，best-effort）
		if err := s.videoRepo.IncrementStat(ctx, parent.VideoID, "comment_count", 1); err != nil {
			slog.Warn("incr comment_count failed", "video_id", parent.VideoID, "error", err)
		}
		return &PostCommentResp{CommentID: comment.ID.Hex()}, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, commentSourceError(err)
	}
	if err := s.commentRepo.Create(ctx, comment); err != nil {
		return nil, commentSourceError(err)
	}
	// 评论计数 +1（best-effort，不影响主流程）
	if err := s.videoRepo.IncrementStat(ctx, req.VideoID, "comment_count", 1); err != nil {
		slog.Warn("incr comment_count failed", "video_id", req.VideoID, "error", err)
	}
	s.notifyCommentTarget(ctx, req, video)

	return &PostCommentResp{CommentID: comment.ID.Hex()}, nil
}

func (s *CommentService) notifyCommentTarget(ctx context.Context, req *PostCommentReq, v *model.Video) {
	s.notify(ctx, events.Event{
		Type:        events.NotifComment,
		RecipientID: v.AuthorID,
		ActorID:     req.UserID,
		TargetID:    req.VideoID,
		TargetType:  events.TargetVideo,
		Content:     req.Content,
	})
}

// ——— List ———

type CommentListReq struct {
	VideoID  string `json:"video_id"`
	Page     int64  `json:"page"`
	PageSize int64  `json:"page_size"`
}

type CommentDetail struct {
	*model.Comment
	IsLiked        bool `json:"is_liked"`
	LikeStateKnown bool `json:"like_state_known"`
}

type CommentListResp struct {
	List  []*CommentDetail `json:"list"`
	Total int64            `json:"total"`
	Page  int64            `json:"page"`
}

func (s *CommentService) GetComments(ctx context.Context, req *CommentListReq, currentUserID string) (*CommentListResp, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if req == nil || currentUserID != "" && !ValidLikeActor(currentUserID) {
		return nil, repo.ErrCommentInvalid
	}
	if !validCommentTarget(req.VideoID) {
		return nil, repo.ErrCommentInvalid
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Page > 100 {
		req.Page = 100 // 深分页上限：防御 Mongo skip 放大
	}
	if req.PageSize <= 0 || req.PageSize > 50 {
		req.PageSize = 20
	}
	if _, err := s.currentCommentVideo(ctx, req.VideoID); err != nil {
		return nil, err
	}
	if s.commentRepo == nil {
		return nil, repo.ErrCommentUnavailable
	}

	comments, total, err := s.commentRepo.GetByVideoID(ctx, req.VideoID, req.Page, req.PageSize)
	if err != nil {
		return nil, commentSourceError(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, commentSourceError(err)
	}

	likesCtx, likesCancel := context.WithTimeout(ctx, time.Second)
	defer likesCancel()
	details := make([]*CommentDetail, 0, len(comments))
	for _, c := range comments {
		copyComment := *c
		d := &CommentDetail{Comment: &copyComment}
		if s.likeService != nil && s.likeService.DurableLikesEnabled() {
			count, err := s.likeService.GetLikeCount(ctx, c.ID.Hex(), "comment")
			if err != nil {
				return nil, err
			}
			d.LikeCount = count
			d.LikeStateKnown = true
			if currentUserID != "" {
				liked, err := s.likeService.IsLiked(ctx, currentUserID, c.ID.Hex(), "comment")
				if err != nil {
					return nil, err
				}
				d.IsLiked = liked
			}
		} else if likesCtx.Err() == nil {
			if reader, ok := s.commentRepo.(repo.LegacyCommentLikeReader); ok {
				snapshot, err := reader.ReadLegacyLikes(likesCtx, c.ID.Hex(), currentUserID)
				if likesCtx.Err() == nil && err == nil && snapshot != nil && snapshot.CommentID == c.ID.Hex() && snapshot.Count >= 0 && snapshot.Version >= 0 {
					d.LikeStateKnown, d.LikeCount, d.IsLiked = true, snapshot.Count, snapshot.Active
				}
			}
		}
		details = append(details, d)
	}

	return &CommentListResp{List: details, Total: total, Page: req.Page}, nil
}

// ——— Like ———

type CommentLikeReq struct {
	CommentID string `json:"comment_id"`
	UserID    string `json:"user_id"`
}

func (s *CommentService) LikeComment(ctx context.Context, req *CommentLikeReq) error {
	if req == nil {
		return repo.ErrLikeInvalid
	}
	return s.commentLikeService().Like(ctx, &LikeReq{UserID: req.UserID, TargetID: req.CommentID, TargetType: "comment"})
}

func (s *CommentService) UnlikeComment(ctx context.Context, req *CommentLikeReq) error {
	if req == nil {
		return repo.ErrLikeInvalid
	}
	return s.commentLikeService().Unlike(ctx, &LikeReq{UserID: req.UserID, TargetID: req.CommentID, TargetType: "comment"})
}

func (s *CommentService) commentLikeService() *LikeService {
	if s.likeService != nil {
		return s.likeService
	}
	return NewLikeService(nil, s.videoRepo, s.commentRepo, s.notifier)
}

// ——— Report ———

type ReportCommentReq struct {
	CommentID string `json:"comment_id"`
	Reason    string `json:"reason"`
}

func (s *CommentService) ReportComment(ctx context.Context, req *ReportCommentReq) error {
	if req.CommentID == "" {
		return fmt.Errorf("comment_id is required")
	}
	if req.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if utf8.RuneCountInString(req.Reason) > 500 {
		return fmt.Errorf("reason too long (max 500)")
	}
	videoID, hidden, err := s.commentRepo.Report(ctx, req.CommentID, req.Reason)
	if err != nil {
		return err
	}
	if hidden && videoID != "" {
		if decErr := s.videoRepo.IncrementStat(ctx, videoID, "comment_count", -1); decErr != nil {
			slog.Warn("decr comment_count after report failed", "video_id", videoID, "error", decErr)
		}
	}
	return nil
}

// ——— Restore (admin) ———

// RestoreComment reverses a report-driven hide and restores the exact counter
// once. Idempotent: an already-active/deleted comment is left untouched.
func (s *CommentService) RestoreComment(ctx context.Context, commentID string) (bool, error) {
	if commentID == "" {
		return false, fmt.Errorf("%w: comment_id is required", ErrBadRequest)
	}
	videoID, restored, err := s.commentRepo.Restore(ctx, commentID)
	if err != nil {
		return false, err
	}
	if restored && videoID != "" {
		if incErr := s.videoRepo.IncrementStat(ctx, videoID, "comment_count", 1); incErr != nil {
			slog.Warn("incr comment_count after restore failed", "video_id", videoID, "error", incErr)
		}
	}
	return restored, nil
}

// ——— Delete ———

func (s *CommentService) DeleteComment(ctx context.Context, commentID, userID string) error {
	return s.deleteCurrentComment(ctx, commentID, userID)
}
