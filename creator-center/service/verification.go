package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	usermodel "feedvideo.local/user/model"
	"gorm.io/gorm"
)

var (
	ErrVerificationInvalid        = errors.New("invalid verification target")
	ErrVerificationTargetNotFound = errors.New("verification target not found")
	ErrVerificationUnavailable    = errors.New("creator verification unavailable")
)

type existingProfileVerifier interface {
	SetVerifiedExisting(context.Context, int64, bool) error
}

func (s *CreatorService) SetVerificationTargetReader(fn func(context.Context, int64) (*usermodel.User, error)) {
	s.verificationTargetReader = fn
}

// Account existence is a current primary read, distinct from profile presence
// or the actor's status cache. Neither unknown result can authorize an upsert.
func (s *CreatorService) SetVerified(ctx context.Context, targetUID int64, verified bool) error {
	if targetUID <= 0 {
		return ErrVerificationInvalid
	}
	if ctx == nil || s == nil || s.verificationTargetReader == nil {
		return ErrVerificationUnavailable
	}
	writer, ok := s.userRepo.(existingProfileVerifier)
	if !ok {
		return ErrVerificationUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, err)
	}
	sqlCtx, sqlCancel := context.WithTimeout(ctx, time.Second)
	user, err := s.verificationTargetReader(sqlCtx, targetUID)
	sqlErr := sqlCtx.Err()
	sqlCancel()
	if sqlErr != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, sqlErr)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, err)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrVerificationTargetNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, err)
	}
	if user == nil || uint64(user.ID) != uint64(targetUID) {
		return ErrVerificationUnavailable
	}
	if user.DeletedAt.Valid {
		return ErrVerificationTargetNotFound
	}
	switch user.Status {
	case 0, 1, 2:
	default:
		return ErrVerificationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, err)
	}
	if err := writer.SetVerifiedExisting(ctx, targetUID, verified); err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationUnavailable, err)
	}
	return nil
}
