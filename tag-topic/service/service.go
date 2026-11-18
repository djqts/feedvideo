package service

import (
	"context"
	"errors"

	"feedvideo.local/tag-topic/model"
	"feedvideo.local/tag-topic/repo"
	videorepo "feedvideo.local/video/repo"

	"go.mongodb.org/mongo-driver/mongo"
)

// topicRepo is the minimal repository dependency of TagTopicService.
// *repo.TopicRepo satisfies it.
type topicRepo interface {
	Create(ctx context.Context, name, desc string) (*model.Topic, error)
	FindByName(ctx context.Context, name string) (*model.Topic, error)
	FindByID(ctx context.Context, id string) (*model.Topic, error)
	CountVideos(ctx context.Context, name string) (int64, error)
	IncrHot(ctx context.Context, tag string) error
	TopHot(ctx context.Context, n int64) ([]struct {
		Name  string
		Score float64
	}, error)
	EnsureIndexes(ctx context.Context) error
}

type TagTopicService struct {
	repo      topicRepo
	videoRepo videorepo.VideoRepo
}

func NewTagTopicService(repo topicRepo, videoRepo videorepo.VideoRepo) *TagTopicService {
	return &TagTopicService{repo: repo, videoRepo: videoRepo}
}

func (s *TagTopicService) Create(ctx context.Context, name, desc string) (*model.Topic, error) {
	return s.repo.Create(ctx, name, desc)
}

func (s *TagTopicService) GetDetail(ctx context.Context, name string, page, size int64) (*model.TopicDetail, error) {
	name = repo.NormalizeName(name)
	if name == "" {
		return nil, nil
	}
	t, err := s.repo.FindByName(ctx, name)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	videos, total, err := s.videoRepo.SearchByTags(ctx, []string{name}, page, size)
	if err != nil {
		return nil, err
	}
	if t == nil {
		if total == 0 {
			return nil, nil
		}
		t = &model.Topic{Name: name}
	}
	return &model.TopicDetail{Topic: t, Videos: videos, TotalVideos: total, Page: page, HasMore: int64(len(videos)) >= size && total > page*size}, nil
}

func (s *TagTopicService) GetDetailByID(ctx context.Context, topicID string, page, size int64) (*model.TopicDetail, error) {
	t, err := s.repo.FindByID(ctx, topicID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	videos, total, err := s.videoRepo.SearchByTags(ctx, []string{t.Name}, page, size)
	if err != nil {
		return nil, err
	}
	return &model.TopicDetail{Topic: t, Videos: videos, TotalVideos: total, Page: page, HasMore: int64(len(videos)) >= size && total > page*size}, nil
}

func (s *TagTopicService) HotTopics(ctx context.Context, n int64) ([]*model.Topic, error) {
	hot, err := s.repo.TopHot(ctx, n)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Topic, 0, len(hot))
	for _, h := range hot {
		t, _ := s.repo.FindByName(ctx, h.Name)
		if t == nil {
			t = &model.Topic{Name: h.Name}
		}
		out = append(out, t)
	}
	return out, nil
}

// IncrHot 实现 video/service.TopicNotifier 接口。
func (s *TagTopicService) IncrHot(ctx context.Context, tag string) error {
	return s.repo.IncrHot(ctx, tag)
}
