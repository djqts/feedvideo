package service

import (
	"context"

	"feedvideo.local/moderation/model"
)

// moderationRepo is the minimal repository dependency of ModerationService.
// *repo.ModerationRepo satisfies it.
type moderationRepo interface {
	ListReports(ctx context.Context, page, size int64) ([]*model.ReportItem, int64, error)
	UpdateUserStatus(ctx context.Context, uid int64, status int) error
}

type ModerationService struct {
	repo              moderationRepo
	onUserStatusChange func(uid int64, status int)
}

func NewModerationService(repo moderationRepo) *ModerationService {
	return &ModerationService{repo: repo}
}

// SetUserStatusHook 注入状态变更后的回调（网关按 status 吊销/恢复 refresh 族、清状态缓存）。
// status 语义：0=封禁禁用，1=正常，2=禁言。
func (s *ModerationService) SetUserStatusHook(fn func(uid int64, status int)) {
	s.onUserStatusChange = fn
}

func (s *ModerationService) ListReports(ctx context.Context, page, size int64) ([]*model.ReportItem, int64, error) {
	return s.repo.ListReports(ctx, page, size)
}

func (s *ModerationService) BanUser(ctx context.Context, uid int64, banned bool) error {
	if banned {
		if err := s.repo.UpdateUserStatus(ctx, uid, 0); err != nil {
			return err
		}
		if s.onUserStatusChange != nil {
			s.onUserStatusChange(uid, 0)
		}
		return nil
	}
	if err := s.repo.UpdateUserStatus(ctx, uid, 1); err != nil {
		return err
	}
	if s.onUserStatusChange != nil {
		s.onUserStatusChange(uid, 1)
	}
	return nil
}

func (s *ModerationService) MuteUser(ctx context.Context, uid int64, muted bool) error {
	var status int
	if muted {
		status = 2
	} else {
		status = 1
	}
	if err := s.repo.UpdateUserStatus(ctx, uid, status); err != nil {
		return err
	}
	if s.onUserStatusChange != nil {
		s.onUserStatusChange(uid, status)
	}
	return nil
}
