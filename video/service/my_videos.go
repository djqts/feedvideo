package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ListMyVideos lists the signed-in author's own videos in every review state,
// newest first.
//
// The public list endpoint only exposes approved videos, so a submission would
// disappear from the client the moment it is created (it starts as pending).
// The publish screen needs exactly that window, hence a separate owner-scoped
// read.
func (s *VideoService) ListMyVideos(ctx context.Context, authorID string, page, pageSize int64, beforeID string) (*SearchResp, error) {
	if authorID == "" {
		return nil, repo.ErrVideoReadInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	if page <= 0 {
		page = 1
	}
	if page > 100 {
		page = 100
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 20
	}
	if s.videoRepo == nil {
		return nil, repo.ErrVideoReadUnavailable
	}

	base := bson.M{
		"author_id":  authorID,
		"deleted_at": bson.M{"$exists": false},
	}
	filter := base
	cursor := beforeID != ""
	if cursor {
		beforeOID, oidErr := primitive.ObjectIDFromHex(beforeID)
		if oidErr != nil {
			return nil, repo.ErrVideoReadInvalid
		}
		filter = bson.M{
			"author_id":  authorID,
			"deleted_at": bson.M{"$exists": false},
			"_id":        bson.M{"$lt": beforeOID},
		}
		page = 1
	}
	videos, total, err := s.videoRepo.List(ctx, filter, page, pageSize)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		if errors.Is(err, repo.ErrVideoReadInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	if total < 0 {
		return nil, repo.ErrVideoReadUnavailable
	}

	// Counters live in a separate document per video; without them the list
	// would claim every video has zero plays.
	ids := make([]string, 0, len(videos))
	for _, video := range videos {
		if video != nil {
			ids = append(ids, video.ID.Hex())
		}
	}
	stats := map[string]*model.VideoStats{}
	if len(ids) > 0 {
		if fetched, statsErr := s.videoRepo.BatchGetStats(ctx, ids); statsErr == nil {
			stats = fetched
		}
	}

	if cursor {
		// The cursor filter counts only what is left; the header still wants the
		// overall number, so ask for it once more.
		if _, overall, countErr := s.videoRepo.List(ctx, base, 1, 1); countErr == nil {
			total = overall
		}
	}

	details := make([]*VideoDetail, 0, len(videos))
	for _, video := range videos {
		if video == nil {
			continue
		}
		details = append(details, &VideoDetail{Video: video, Stats: stats[video.ID.Hex()]})
	}
	resp := &SearchResp{List: details, Total: total, Page: page}
	if cursor {
		resp.HasMore = int64(len(details)) >= pageSize
	} else {
		resp.HasMore = page*pageSize < total
	}
	if len(details) > 0 {
		resp.NextBefore = details[len(details)-1].ID.Hex()
	}
	return resp, nil
}
