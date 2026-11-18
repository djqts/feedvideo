package service

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"

	feedmodel "feedvideo.local/feed/model"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
)

// Hot treats ranking and public-list bodies as candidates. Current source facts
// must succeed for the whole batch before statistics or a response can escape.
func (s *FeedService) Hot(ctx context.Context, size int64) (*feedmodel.FeedResp, error) {
	if size < 1 || size > 50 {
		return nil, videorepo.ErrVideoReadInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
	}
	if s == nil || s.videoRepo == nil {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	videos, err := s.videoRepo.GetHotVideos(ctx, 0, size-1)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
	}
	if len(videos) == 0 {
		lister, ok := s.videoRepo.(videorepo.PublicVideoLister)
		if !ok {
			return nil, videorepo.ErrVideoReadUnavailable
		}
		videos, _, err = lister.ListPublicVideos(ctx, 1, size)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, ctxErr)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
		}
	}
	videos, err = s.currentHotVideos(ctx, videos, size)
	if err != nil {
		return nil, err
	}
	items, err := s.toItems(ctx, videos)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		return nil, err
	}
	return &feedmodel.FeedResp{List: items, HasMore: int64(len(items)) >= size}, nil
}

// Preserve the raw position budget even when string candidates are malformed.
func (s *FeedService) currentIDs(ctx context.Context, ids []string, size int64) ([]*videomodel.Video, error) {
	if int64(len(ids)) > size || len(ids) > 50 {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	candidates := make([]*videomodel.Video, len(ids))
	for i, id := range ids {
		oid, err := primitive.ObjectIDFromHex(id)
		if err == nil && !oid.IsZero() {
			candidates[i] = &videomodel.Video{ID: oid}
		}
	}
	return s.currentHotVideos(ctx, candidates, size)
}

func (s *FeedService) currentHotVideos(ctx context.Context, candidates []*videomodel.Video, size int64) ([]*videomodel.Video, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
	}
	if int64(len(candidates)) > size || len(candidates) > 50 {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	ids := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.ID.IsZero() {
			continue
		}
		id := candidate.ID.Hex()
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	visible := make([]*videomodel.Video, 0, len(ids))
	if len(candidates) == 0 {
		return visible, nil
	}
	reader, ok := s.videoRepo.(videorepo.CurrentVideoReader)
	if !ok {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	if len(ids) == 0 {
		return visible, nil
	}
	current, err := reader.BatchGetCurrentVideos(ctx, ids)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
	}
	if current == nil {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	// Validate every returned identity before accepting even the first body.
	for id, video := range current {
		if _, requested := seen[id]; !requested || video == nil || video.ID.IsZero() || video.ID.Hex() != id {
			return nil, videorepo.ErrVideoReadUnavailable
		}
	}
	for _, id := range ids {
		if video := current[id]; videomodel.IsPubliclyVisible(video) {
			visible = append(visible, video)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
	}
	return visible, nil
}
