package service

import (
	"context"
	"fmt"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Public providers supply candidates only. Resolve current source bodies before
// statistics, retaining candidate order within the caller's original bound.
func (s *VideoService) currentPublicVideos(ctx context.Context, candidates []*model.Video, authorID string, limit int64) ([]*model.Video, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	if int64(len(candidates)) > limit || len(candidates) > 200 {
		return nil, repo.ErrVideoReadUnavailable
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
	visible := make([]*model.Video, 0, len(ids))
	if len(candidates) == 0 {
		return visible, nil
	}
	reader, ok := s.videoRepo.(repo.CurrentVideoReader)
	if !ok {
		return nil, repo.ErrVideoReadUnavailable
	}
	if len(ids) == 0 {
		return visible, nil
	}
	current, err := reader.BatchGetCurrentVideos(ctx, ids)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	if current == nil {
		return nil, repo.ErrVideoReadUnavailable
	}
	// Validate the entire returned map before accepting any body or reading stats.
	for id, v := range current {
		if _, requested := seen[id]; !requested {
			return nil, repo.ErrVideoReadUnavailable
		}
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil || v == nil || v.ID != oid {
			return nil, repo.ErrVideoReadUnavailable
		}
	}
	for _, id := range ids {
		v, exists := current[id]
		if !exists {
			continue
		}
		if model.IsPubliclyVisible(v) && (authorID == "" || v.AuthorID == authorID) {
			visible = append(visible, v)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	return visible, nil
}
