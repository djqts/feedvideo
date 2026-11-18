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

func validCommentTarget(id string) bool {
	oid, err := primitive.ObjectIDFromHex(id)
	return err == nil && !oid.IsZero() && oid.Hex() == id
}

func commentSourceError(err error) error {
	if errors.Is(err, repo.ErrCommentInvalid) || errors.Is(err, repo.ErrCommentNotFound) || errors.Is(err, repo.ErrCommentUnavailable) {
		return err
	}
	if errors.Is(err, repo.ErrVideoNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
		return repo.ErrCommentNotFound
	}
	return fmt.Errorf("%w: %w", repo.ErrCommentUnavailable, err)
}

func (s *CommentService) currentCommentVideo(ctx context.Context, id string) (*model.Video, error) {
	if !validCommentTarget(id) {
		return nil, repo.ErrCommentInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, commentSourceError(err)
	}
	reader, ok := s.videoRepo.(repo.CurrentVideoReader)
	if !ok {
		return nil, repo.ErrCommentUnavailable
	}
	video, err := reader.GetCurrentByID(ctx, id)
	if err := ctx.Err(); err != nil {
		return nil, commentSourceError(err)
	}
	if err != nil {
		return nil, commentSourceError(err)
	}
	if video == nil || video.ID.Hex() != id {
		return nil, repo.ErrCommentUnavailable
	}
	if !model.IsPubliclyVisible(video) {
		return nil, repo.ErrCommentNotFound
	}
	return video, nil
}
