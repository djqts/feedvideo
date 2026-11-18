package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const maxBackgroundBuilds = 16

// Admission and closing share one lock. Active includes preparation and owner
// cleanup, so waiting cannot miss an admitted job that has not been launched.
type backgroundBuilds struct {
	mu               sync.Mutex
	ctx              context.Context
	cancel           context.CancelFunc
	started, closing bool
	active           int
	done             chan struct{}
	closed           bool
}

func newBackgroundBuilds() *backgroundBuilds {
	return &backgroundBuilds{done: make(chan struct{})}
}

func (s *FeedService) StartBackground(parent context.Context) error {
	if parent == nil {
		return errors.New("feed background context unavailable")
	}
	b := s.background
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started || b.closing {
		return errors.New("feed background cannot be restarted")
	}
	if err := parent.Err(); err != nil {
		b.closeLocked()
		return err
	}
	b.ctx, b.cancel = context.WithCancel(parent)
	b.started = true
	go func() {
		<-b.ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		b.closeLocked()
	}()
	return nil
}

func (b *backgroundBuilds) closeLocked() {
	if !b.closing {
		b.closing = true
		if b.cancel != nil {
			b.cancel()
		}
	}
	if b.active == 0 && !b.closed {
		b.closed = true
		close(b.done)
	}
}

func (b *backgroundBuilds) reserve() (context.Context, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started || b.closing || b.ctx.Err() != nil || b.active == maxBackgroundBuilds {
		return nil, nil, false
	}
	b.active++
	return b.ctx, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.active--
		if b.ctx.Err() != nil || b.closing {
			b.closeLocked()
		}
	}, true
}

func (s *FeedService) StopBackground(ctx context.Context) error {
	b := s.background
	b.mu.Lock()
	b.closeLocked()
	b.mu.Unlock()
	return s.WaitBackground(ctx)
}

func (s *FeedService) WaitBackground(ctx context.Context) error {
	if ctx == nil {
		return errors.New("feed background wait context unavailable")
	}
	b := s.background
	b.mu.Lock()
	if !b.started && !b.closing {
		b.mu.Unlock()
		return errors.New("feed background has not started")
	}
	done := b.done
	b.mu.Unlock()
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The caller already owns the Redis lease. Refused admission still releases
// that owner, and never writes a cooldown or starts a goroutine.
func (s *FeedService) startStaleBuild(request context.Context, uid string, uid64 int64, token string) (result error) {
	parent, finish, ok := s.background.reserve()
	if !ok {
		s.releaseBuild(request, uid, token)
		return nil
	}
	transferred := false
	defer func() {
		if !transferred {
			s.releaseBuild(request, uid, token)
			finish()
		}
		// Only the request's own cancellation fails its response. Stopping the
		// background lifecycle continues the established stale-pool read.
		if err := request.Err(); err != nil {
			result = err
		} else if parent.Err() != nil {
			result = nil
		}
	}()
	prepare, cancelPrepare := context.WithCancel(request)
	stop := context.AfterFunc(parent, cancelPrepare)
	defer stop()
	defer cancelPrepare()
	if err := parent.Err(); err != nil {
		return nil
	}
	build, err := s.feedRepo.BeginPoolBuild(prepare, uid, token)
	if err != nil {
		return err
	}
	if parent.Err() != nil {
		return nil
	}
	if err := s.feedRepo.SetRetryCooldown(prepare, uid, token); err != nil {
		return err
	}
	if err := request.Err(); err != nil {
		return err
	}
	if parent.Err() != nil {
		return nil
	}
	transferred = true
	go func() {
		defer finish()
		defer func() {
			if recover() != nil {
				slog.Warn("async rebuild panicked", "uid", uid)
			}
		}()
		defer s.releaseBuild(parent, uid, token)
		bg, cancel := context.WithTimeout(parent, time.Duration(s.cfg.BuildTimeoutMs)*time.Millisecond)
		defer cancel()
		if err := s.buildPool(bg, uid, uid64, build); err != nil {
			slog.Warn("async rebuild failed", "uid", uid, "error", err)
		}
	}()
	return nil
}
