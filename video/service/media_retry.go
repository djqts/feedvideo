package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedvideo.local/video/repo"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

var (
	ErrMediaUnauthenticated = errors.New("authentication required")
	ErrMediaConflict        = errors.New("media job cannot be retried in its current state")
	ErrMediaNotFound        = errors.New("video not found")
)

func (s *VideoService) SetMediaJobStore(store repo.MediaJobStore) { s.mediaJobStore = store }

// RetryMedia validates the current Mongo fact before changing one failed job.
func (s *VideoService) RetryMedia(ctx context.Context, videoID, actorID string) error {
	if actorID == "" {
		return ErrMediaUnauthenticated
	}
	if _, err := primitive.ObjectIDFromHex(videoID); err != nil {
		return ErrBadRequest
	}
	if !s.mediaJobsEnabled || s.mediaJobStore == nil {
		return ErrMediaUnavailable
	}
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	video, err := s.mediaJobStore.Get(readCtx, videoID)
	readCancel()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrMediaNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: read media state: %v", ErrMediaUnavailable, err)
	}
	if video == nil || video.Status == "deleted" || video.DeletedAt != nil {
		return ErrMediaNotFound
	}
	if video.AuthorID != actorID {
		return ErrForbidden
	}
	job := video.MediaJob
	if job == nil || job.Status != "failed" || job.ContentVersion < 1 || job.ContentVersion != video.ContentVersion || job.SourceKind != "object" {
		return ErrMediaConflict
	}
	jobID, err := uuid.Parse(job.JobID)
	if err != nil || jobID.Version() != 4 || jobID.String() != job.JobID {
		return ErrMediaConflict
	}
	sourceURL := "/storage/" + job.SourceBucket + "/" + job.SourceObject
	if _, err := ValidateMediaSourceReference(sourceURL, s.videoBucket, actorID); err != nil {
		return ErrMediaConflict
	}
	if s.sourceValidator == nil {
		return ErrMediaUnavailable
	}
	statCtx, statCancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.sourceValidator.ValidateMediaSource(statCtx, job.SourceBucket, job.SourceObject)
	statCancel()
	if err != nil {
		return fmt.Errorf("%w: validate media source: %v", ErrMediaUnavailable, err)
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
	matched, err := s.mediaJobStore.RetryFailed(writeCtx, videoID, actorID, job.JobID, job.ContentVersion, time.Now().UTC())
	writeCancel()
	if err != nil {
		return fmt.Errorf("%w: retry media job: %v", ErrMediaUnavailable, err)
	}
	if !matched {
		return ErrMediaConflict
	}
	return nil
}
