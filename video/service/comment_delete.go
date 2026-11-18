package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
)

func commentDeleteError(err error) error {
	if errors.Is(err, repo.ErrCommentForbidden) {
		return ErrForbidden
	}
	return commentSourceError(err)
}

func validDeleteReceipt(c *model.Comment, id, actor, videoID string) bool {
	return c != nil && c.ID.Hex() == id && c.UserID == actor && c.VideoID == videoID && c.ParentID == ""
}

func (s *CommentService) deleteCurrentComment(ctx context.Context, id, actor string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !validCommentTarget(id) || !ValidLikeActor(actor) {
		return repo.ErrCommentInvalid
	}
	if err := ctx.Err(); err != nil {
		return commentDeleteError(err)
	}
	if s.commentRepo == nil {
		return repo.ErrCommentUnavailable
	}
	current, err := s.commentRepo.GetByID(ctx, id)
	if err != nil {
		return commentDeleteError(err)
	}
	if err := ctx.Err(); err != nil {
		return commentDeleteError(err)
	}
	if current == nil || current.ID.Hex() != id || !validCommentTarget(current.VideoID) || !ValidLikeActor(current.UserID) {
		return repo.ErrCommentUnavailable
	}
	if current.UserID != actor {
		return ErrForbidden
	}
	if current.ParentID != "" {
		return repo.ErrCommentNotFound
	}
	if current.Status == "hidden" {
		return nil
	}
	if !model.IsVisibleComment(current) {
		return repo.ErrCommentNotFound
	}
	writer, ok := s.commentRepo.(repo.CommentDeleteWriter)
	if !ok {
		return repo.ErrCommentUnavailable
	}
	if err := ctx.Err(); err != nil {
		return commentDeleteError(err)
	}
	before, changed, err := writer.DeleteCurrent(ctx, id, actor, current.VideoID)
	if err != nil {
		return commentDeleteError(err)
	}
	if !validDeleteReceipt(before, id, actor, current.VideoID) {
		return repo.ErrCommentUnavailable
	}
	if !changed {
		if before.Status != "hidden" {
			return repo.ErrCommentUnavailable
		}
		return nil
	}
	if !model.IsVisibleComment(before) {
		return repo.ErrCommentUnavailable
	}
	// Owner cleanup does not require a current public video. Count only the
	// atomic original image; derived stats may fail after the primary commit.
	delta := -1 - int64(len(before.Replies))
	if s.videoRepo != nil {
		if err := s.videoRepo.IncrementStat(ctx, before.VideoID, "comment_count", delta); err != nil {
			slog.Warn("decr comment_count failed", "video_id", before.VideoID, "error", err)
		}
	}
	return nil
}
