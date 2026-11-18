package service

import (
	"context"
	"errors"
	"time"

	feedrepo "feedvideo.local/feed/repo"
	videorepo "feedvideo.local/video/repo"
)

// The renewal goroutine belongs to this build: every return and panic cancels
// and joins it before the caller releases the owner or returns background slots.
func (s *FeedService) buildPool(ctx context.Context, uid string, uid64 int64, build *feedrepo.PoolBuild) (result error) {
	if ctx == nil {
		return videorepo.ErrVideoReadUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.feedRepo == nil {
		return videorepo.ErrVideoReadUnavailable
	}
	if err := s.feedRepo.RenewPoolBuild(ctx, uid, build); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.Join(feedrepo.ErrPoolBuildRejected, err)
	}
	leased, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var renewalErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-leased.Done():
				return
			case <-ticker.C:
				if err := s.feedRepo.RenewPoolBuild(leased, uid, build); err != nil {
					if leased.Err() == nil {
						renewalErr = err
						cancel()
					}
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-done // Also joins an owned socket that was canceled in flight.
		if err := ctx.Err(); err != nil {
			result = err
		} else if renewalErr != nil {
			result = errors.Join(feedrepo.ErrPoolBuildRejected, renewalErr)
		}
	}()
	return s.buildPoolCandidates(leased, uid, uid64, build)
}
