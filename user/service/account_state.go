package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// CurrentAccountStatus uses the primary auth repository without consulting or
// populating the UI cache. A status-source failure is distinct from a known ban.
func (s *UserService) CurrentAccountStatus(ctx context.Context, actor string) (int, error) {
	id, err := strconv.ParseInt(actor, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != actor {
		return 0, errors.New("invalid account identity")
	}
	if ctx == nil || s == nil || s.auth == nil {
		return 0, errors.New("account source unavailable")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	user, err := s.auth.GetUserByID(ctx, id)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if user == nil || uint64(user.ID) != uint64(id) {
		return 0, errors.New("account source returned invalid identity")
	}
	switch user.Status {
	case 0, 1, 2:
		return user.Status, nil
	default:
		return 0, errors.New("account source returned unknown status")
	}
}
