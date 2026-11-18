package service

import (
	"context"
	"errors"
	events "feedvideo.local/shared-events"
	"gorm.io/gorm"
	"strconv"
)

// WatchAccountActive checks the primary account without the UI status cache.
func (s *UserService) WatchAccountActive(ctx context.Context, actor string) (bool, error) {
	if !events.WatchActorValid(actor) {
		return false, nil
	}
	if s == nil || s.auth == nil {
		return false, errors.New("account source unavailable")
	}
	id, _ := strconv.ParseInt(actor, 10, 64)
	user, err := s.auth.GetUserByID(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user != nil && (user.Status == 1 || user.Status == 2), nil
}
