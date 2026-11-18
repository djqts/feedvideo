package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	feedrepo "feedvideo.local/search/repo"
)

// BackgroundOptions bounds memory, consumer concurrency and per-job I/O.
// Zero values use defaults; Now supplies only the deduplication clock.
type BackgroundOptions struct {
	QueueCapacity, Workers, DedupCapacity int
	JobTimeout, DedupWindow               time.Duration
	Now                                   func() time.Time
}

// BackgroundStats is one coherent snapshot. Running means accepting jobs;
// Active may remain nonzero after cancellation for a context-ignoring repo.
// Dropped includes both rejected submissions and accepted jobs discarded on
// shutdown, so it cannot all be subtracted from Accepted.
type BackgroundStats struct {
	Running                                                                                   bool
	Queued, Active                                                                            int
	Accepted, Completed, Failed, Dropped, Deduplicated, MissingIdentity, DedupCapacityDropped uint64
}

type backgroundJob struct {
	q, key string
	data   []byte
	sample bool
}

type backgroundRecorder struct {
	repo             searchRepo
	opts             BackgroundOptions
	mu               sync.Mutex
	queue            chan backgroundJob
	notify           chan struct{}
	done             chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
	started, closing bool
	workers          int
	dedup            map[[32]byte]time.Time
	stats            BackgroundStats
}

func newBackgroundRecorder(repo searchRepo, opts BackgroundOptions) (*backgroundRecorder, error) {
	if opts.QueueCapacity == 0 {
		opts.QueueCapacity = 64
	}
	if opts.Workers == 0 {
		opts.Workers = 2
	}
	if opts.DedupCapacity == 0 {
		opts.DedupCapacity = 4096
	}
	if opts.JobTimeout == 0 {
		opts.JobTimeout = 500 * time.Millisecond
	}
	if opts.DedupWindow == 0 {
		opts.DedupWindow = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.QueueCapacity < 1 || opts.QueueCapacity > 256 {
		return nil, errors.New("search background queue capacity must be 1..256")
	}
	if opts.Workers < 1 || opts.Workers > 8 {
		return nil, errors.New("search background workers must be 1..8")
	}
	if opts.DedupCapacity < 1 || opts.DedupCapacity > 4096 {
		return nil, errors.New("search background dedup capacity must be 1..4096")
	}
	if opts.JobTimeout < time.Millisecond || opts.JobTimeout > 500*time.Millisecond {
		return nil, errors.New("search background job timeout must be 1ms..500ms")
	}
	if opts.DedupWindow < time.Second || opts.DedupWindow > 60*time.Second {
		return nil, errors.New("search background dedup window must be 1s..60s")
	}
	return &backgroundRecorder{
		repo: repo, opts: opts, queue: make(chan backgroundJob, opts.QueueCapacity),
		notify: make(chan struct{}), done: make(chan struct{}), dedup: make(map[[32]byte]time.Time),
	}, nil
}

func (r *backgroundRecorder) Start(parent context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closing {
		return errors.New("search background recorder cannot be restarted")
	}
	if err := parent.Err(); err != nil {
		r.closeLocked()
		return err
	}
	r.ctx, r.cancel = context.WithCancel(parent)
	r.started = true
	r.stats.Running = true
	r.workers = r.opts.Workers
	for i := 0; i < r.workers; i++ {
		go r.work()
	}
	return nil
}

// All channel sends/receives and lifecycle transitions share mu. Consumers
// wait on a broadcast notification, keeping dequeue and Active atomic.
func (r *backgroundRecorder) Submit(q, key string, data []byte, actorKind, actorID string) {
	q = feedrepo.Normalize(q)
	if q == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stats.Running || r.ctx.Err() != nil {
		if r.stats.Running {
			r.closeLocked()
		}
		r.stats.Dropped++
		return
	}
	if len(data) > maxCandidateCacheBytes || len(key) > 1024 || key == "" {
		data = nil
	}
	// Overflow does not claim a dedup key or increment suppression counters.
	if len(r.queue) == cap(r.queue) {
		r.stats.Dropped++
		return
	}
	sample := true
	var digest [32]byte
	now := r.opts.Now()
	if (actorKind != "account" && actorKind != "session") || actorID == "" || len(actorID) > 128 {
		sample = false
		r.stats.MissingIdentity++
	} else {
		// Bounded identities are hashed with an unambiguous length prefix; the table
		// never retains the original account/session identifier.
		digest = sha256.Sum256([]byte(actorKind + "\x00" + strconv.Itoa(len(actorID)) + ":" + actorID + q))
		for k, expiry := range r.dedup {
			if !now.Before(expiry) {
				delete(r.dedup, k)
			}
		}
		if expiry, ok := r.dedup[digest]; ok && now.Before(expiry) {
			sample = false
			r.stats.Deduplicated++
		} else if len(r.dedup) == r.opts.DedupCapacity {
			sample = false
			r.stats.DedupCapacityDropped++
		}
	}
	if !sample && len(data) == 0 {
		return
	}
	// Freeze admitted fields after bounds checks. A short normalized query or
	// cache-key substring may otherwise retain an unbounded caller allocation.
	job := backgroundJob{q: strings.Clone(q), data: append([]byte(nil), data...), sample: sample}
	if len(data) > 0 {
		job.key = strings.Clone(key)
	}
	r.queue <- job
	if sample {
		r.dedup[digest] = now.Add(r.opts.DedupWindow)
	}
	r.stats.Accepted++
	r.stats.Queued++
	close(r.notify)
	r.notify = make(chan struct{})
}

func (r *backgroundRecorder) work() {
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.workers--
		if r.workers == 0 {
			close(r.done)
		}
	}()
	for {
		r.mu.Lock()
		if r.ctx.Err() != nil {
			r.closeLocked()
		}
		if r.closing {
			r.mu.Unlock()
			return
		}
		select {
		case job := <-r.queue:
			r.stats.Queued--
			r.stats.Active++
			r.mu.Unlock()
			failed := r.runJob(job)
			r.mu.Lock()
			r.stats.Active--
			r.stats.Completed++
			if failed {
				r.stats.Failed++
			}
			r.mu.Unlock()
		default:
			notify, ctx := r.notify, r.ctx
			r.mu.Unlock()
			select {
			case <-notify:
			case <-ctx.Done():
			}
		}
	}
}

func (r *backgroundRecorder) runJob(job backgroundJob) (failed bool) {
	ctx, cancel := context.WithTimeout(r.ctx, r.opts.JobTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			failed = true
		}
		if ctx.Err() != nil {
			failed = true
		}
	}()
	if len(job.data) > 0 {
		if ctx.Err() != nil {
			return true
		}
		if r.repo.SetCache(ctx, job.key, job.data) != nil {
			failed = true
		}
	}
	if job.sample {
		if ctx.Err() != nil {
			return true
		}
		if r.repo.IncrHotWord(ctx, job.q) != nil {
			failed = true
		}
		if ctx.Err() != nil {
			return true
		}
		if r.repo.AddSuggest(ctx, job.q) != nil {
			failed = true
		}
	}
	return failed
}

func (r *backgroundRecorder) closeLocked() {
	if r.closing {
		return
	}
	r.closing = true
	r.stats.Running = false
	if r.cancel != nil {
		r.cancel()
	}
	for {
		select {
		case <-r.queue:
			r.stats.Queued--
			r.stats.Dropped++
		default:
			clear(r.dedup)
			close(r.notify)
			if !r.started {
				close(r.done)
			}
			return
		}
	}
}

func (r *backgroundRecorder) Stop(ctx context.Context) error {
	r.mu.Lock()
	r.closeLocked()
	r.mu.Unlock()
	return r.Wait(ctx)
}

func (r *backgroundRecorder) Wait(ctx context.Context) error {
	r.mu.Lock()
	if !r.started && !r.closing {
		r.mu.Unlock()
		return errors.New("search background recorder has not started")
	}
	done := r.done
	r.mu.Unlock()
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

func (r *backgroundRecorder) Stats() BackgroundStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats.Running && r.ctx.Err() != nil {
		r.closeLocked()
	}
	return r.stats
}
