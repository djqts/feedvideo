package scheduler

import (
	"context"
	"errors"
	"feedvideo.local/shared-db/elastic"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"github.com/google/uuid"
	"sync"
	"time"
)

type SearchProjectionSink interface {
	Index(context.Context, *model.Video) error
}
type SearchProjectionStats struct {
	Succeeded, Failed, Conflicts, Expired int64
	Pending                               int64
	Oldest                                time.Time
	PendingKnown                          bool
}
type SearchProjectionWorker struct {
	store repo.SearchProjectionStore
	sink  SearchProjectionSink
	owner string
	now   func() time.Time
	mu    sync.RWMutex
	stats SearchProjectionStats
}

func NewSearchProjectionWorker(store repo.SearchProjectionStore, sink SearchProjectionSink) *SearchProjectionWorker {
	return &SearchProjectionWorker{store: store, sink: sink, owner: uuid.NewString(), now: time.Now, stats: SearchProjectionStats{Pending: -1}}
}
func (w *SearchProjectionWorker) Stats() SearchProjectionStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.stats
}
func (w *SearchProjectionWorker) Pass(parent context.Context) (passErr error) {
	if w == nil || w.store == nil || w.sink == nil {
		return repo.ErrSearchProjectionUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	clockDeadline := w.now().Add(5 * time.Second)
	remaining := func() time.Duration {
		d := clockDeadline.Sub(w.now())
		if wall, ok := ctx.Deadline(); ok && time.Until(wall) < d {
			d = time.Until(wall)
		}
		return d
	}
	defer func() {
		if ctx.Err() != nil || remaining() <= 0 {
			w.mu.Lock()
			w.stats.PendingKnown = false
			w.mu.Unlock()
			return
		}
		pending, oldest, err := w.store.Pending(ctx)
		w.mu.Lock()
		w.stats.PendingKnown = err == nil
		if err == nil {
			w.stats.Pending, w.stats.Oldest = pending, oldest
		}
		w.mu.Unlock()
		passErr = errors.Join(passErr, err)
	}()
	for i := 0; i < 100; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if remaining() <= 0 {
			return context.DeadlineExceeded
		}
		lease, err := w.store.Claim(ctx, w.owner, w.now(), time.Minute)
		if err != nil {
			return err
		}
		if lease == nil {
			return nil
		}
		if lease.Video == nil || lease.Video.ID.IsZero() || lease.Video.Version <= 0 || lease.SourceVersion != lease.Video.Version || lease.Token <= 0 || lease.Owner != w.owner || !lease.Until.After(w.now()) {
			return repo.ErrSearchProjectionInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Preserve at least half a second for the Mongo CAS in the shared pass budget.
		indexBudget := remaining() - 500*time.Millisecond
		if indexBudget <= 0 {
			return context.DeadlineExceeded
		}
		if indexBudget > 2*time.Second {
			indexBudget = 2 * time.Second
		}
		indexCtx, indexCancel := context.WithTimeout(ctx, indexBudget)
		err = w.sink.Index(indexCtx, projectionSourceCopy(lease.Video))
		indexCancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if remaining() <= 0 {
			return context.DeadlineExceeded
		}
		var ok bool
		if err == nil {
			ok, err = w.store.Complete(ctx, lease, w.now())
			if err != nil {
				return err
			}
			w.mu.Lock()
			if ok {
				w.stats.Succeeded++
			} else {
				w.stats.Expired++
			}
			w.mu.Unlock()
		} else {
			code := "internal_error"
			if errors.Is(err, elastic.ErrVersionConflict) {
				code = "version_conflict"
			} else if errors.Is(err, elastic.ErrUnavailable) || errors.Is(err, repo.ErrSearchProjectionUnavailable) || errors.Is(err, context.DeadlineExceeded) {
				code = "es_unavailable"
			}
			w.mu.Lock()
			w.stats.Failed++
			if code == "version_conflict" {
				w.stats.Conflicts++
			}
			w.mu.Unlock()
			ok, err = w.store.Fail(ctx, lease, w.now(), code)
			if err != nil {
				return err
			}
			if !ok {
				w.mu.Lock()
				w.stats.Expired++
				w.mu.Unlock()
			}
		}
	}
	return nil
}

// The sink receives its own source values; it cannot mutate the lease's CAS identity.
func projectionSourceCopy(v *model.Video) *model.Video {
	cp := *v
	cp.Tags = append([]string(nil), v.Tags...)
	if v.Location != nil {
		geo := *v.Location
		geo.Coordinates = append([]float64(nil), v.Location.Coordinates...)
		cp.Location = &geo
	}
	if v.DeletedAt != nil {
		deleted := *v.DeletedAt
		cp.DeletedAt = &deleted
	}
	if v.SearchProjection != nil {
		projection := *v.SearchProjection
		cp.SearchProjection = &projection
	}
	return &cp
}

func (w *SearchProjectionWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = w.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
