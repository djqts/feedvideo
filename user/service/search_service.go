package service

import (
	"context"

	"feedvideo.local/user/model"
)

// userSearchRepo is the minimal search backend dependency of SearchService.
// *repo.UserSearch satisfies it.
type userSearchRepo interface {
	SearchUsers(ctx context.Context, query string, offset, size int) ([]*model.UserDocument, int64, error)
	IndexUser(ctx context.Context, userID int64, doc *model.UserDocument) error
	DeleteUser(ctx context.Context, userID int64) error
}

type SearchService struct {
	userRepo userSearchRepo
}

func NewSearchService(userRepo userSearchRepo) *SearchService {
	return &SearchService{
		userRepo: userRepo,
	}
}

func (s *SearchService) SearchUsers(ctx context.Context, query string, page, size int) (*model.SearchResponse, error) {
	if size <= 0 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size
	if offset < 0 {
		offset = 0
	}
	users, total, err := s.userRepo.SearchUsers(ctx, query, offset, size)
	if err != nil {
		return nil, err
	}
	if users == nil {
		users = []*model.UserDocument{}
	}
	return &model.SearchResponse{
		Total: int(total),
		Items: users,
	}, nil
}

func (s *SearchService) IndexUser(ctx context.Context, userID int64, doc *model.UserDocument) error {
	return s.userRepo.IndexUser(ctx, userID, doc)
}

func (s *SearchService) DeleteUser(ctx context.Context, userID int64) error {
	return s.userRepo.DeleteUser(ctx, userID)
}
