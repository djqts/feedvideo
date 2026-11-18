package service

import (
	"context"
	"errors"
	"fmt"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func validLikeTarget(targetID, targetType string) bool {
	id, err := primitive.ObjectIDFromHex(targetID)
	return err == nil && !id.IsZero() && id.Hex() == targetID && (targetType == "video" || targetType == "comment")
}

func likeContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", repo.ErrLikeUnavailable, err)
	}
	return nil
}

func likeTargetReadError(err error) error {
	if errors.Is(err, repo.ErrVideoNotFound) || errors.Is(err, repo.ErrCommentNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
		return ErrLikeTargetNotFound
	}
	return fmt.Errorf("%w: %w", repo.ErrLikeUnavailable, err)
}

// Both modes require current source facts before relationship reads or writes.
// A comment must be a visible root belonging to a currently public video.
func (s *LikeService) likeRecipient(ctx context.Context, targetID, targetType string) (string, error) {
	if !validLikeTarget(targetID, targetType) {
		return "", repo.ErrLikeInvalid
	}
	if err := likeContextError(ctx); err != nil {
		return "", err
	}
	videoID, recipient := targetID, ""
	if targetType == "comment" {
		if s.commentRepo == nil {
			return "", repo.ErrLikeUnavailable
		}
		comment, err := s.commentRepo.GetByID(ctx, targetID)
		if canceled := likeContextError(ctx); canceled != nil {
			return "", canceled
		}
		if err != nil {
			return "", likeTargetReadError(err)
		}
		if comment == nil || comment.ID.Hex() != targetID {
			return "", repo.ErrLikeUnavailable
		}
		if !model.IsVisibleComment(comment) || comment.ParentID != "" {
			return "", ErrLikeTargetNotFound
		}
		videoID, recipient = comment.VideoID, comment.UserID
		if !validLikeTarget(videoID, "video") {
			return "", repo.ErrLikeUnavailable
		}
	}
	reader, ok := s.videoRepo.(repo.CurrentVideoReader)
	if !ok {
		return "", repo.ErrLikeUnavailable
	}
	video, err := reader.GetCurrentByID(ctx, videoID)
	if canceled := likeContextError(ctx); canceled != nil {
		return "", canceled
	}
	if err != nil {
		return "", likeTargetReadError(err)
	}
	if video == nil || video.ID.Hex() != videoID {
		return "", repo.ErrLikeUnavailable
	}
	if !model.IsPubliclyVisible(video) {
		return "", ErrLikeTargetNotFound
	}
	if targetType == "video" {
		recipient = video.AuthorID
	}
	return recipient, nil
}
