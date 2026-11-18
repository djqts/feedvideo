package service

import (
	"context"
	"fmt"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
)

// AdminVideoList is the review console payload. It intentionally carries the
// raw moderation fields (status, media_status, author_id) that public lists
// never expose for unapproved content.
type AdminVideoList struct {
	List     []*model.Video `json:"list"`
	Total    int64          `json:"total"`
	Page     int64          `json:"page"`
	PageSize int64          `json:"page_size"`
}

// AdminVideoLister is implemented by the Mongo repository.
type AdminVideoLister interface {
	ListVideosByStatus(ctx context.Context, status string, page, pageSize int64) ([]*model.Video, int64, error)
}

// ListVideosForAdmin returns non-deleted videos in one moderation state.
func (s *VideoService) ListVideosForAdmin(ctx context.Context, status string, page, pageSize int64) (*AdminVideoList, error) {
	switch status {
	case "", "pending":
		status = "pending"
	case "rejected", "approved":
	default:
		return nil, fmt.Errorf("%w: status must be pending, rejected or approved", ErrBadRequest)
	}
	if page <= 0 {
		page = 1
	}
	if page > 100 {
		page = 100
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	lister, ok := s.videoRepo.(AdminVideoLister)
	if !ok {
		return nil, repo.ErrVideoReadUnavailable
	}
	videos, total, err := lister.ListVideosByStatus(ctx, status, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &AdminVideoList{List: videos, Total: total, Page: page, PageSize: pageSize}, nil
}
