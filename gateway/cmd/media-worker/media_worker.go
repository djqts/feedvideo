package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
)

const (
	mediaStoreBudget       = 5 * time.Second
	mediaLeaseTTL          = 60 * time.Second
	mediaHeartbeatInterval = 20 * time.Second
	mediaJobBudget         = 10 * time.Minute
	mediaScanInterval      = 5 * time.Second
)

var errMediaLeaseLost = errors.New("media lease lost")

type mediaProcessor interface {
	Process(context.Context, *model.MediaLease) (*model.MediaResult, error)
	Cleanup(context.Context, *model.MediaLease, *model.MediaResult) error
}

type mediaTickFactory func(time.Duration) (<-chan time.Time, func())

type mediaWorker struct {
	store     repo.MediaJobStore
	processor mediaProcessor
	owner     string
	now       func() time.Time
	ticks     mediaTickFactory
}

func newMediaWorker(store repo.MediaJobStore, processor mediaProcessor, owner string, now func() time.Time, ticks mediaTickFactory) *mediaWorker {
	if now == nil {
		now = time.Now
	}
	if ticks == nil {
		ticks = func(interval time.Duration) (<-chan time.Time, func()) {
			ticker := time.NewTicker(interval)
			return ticker.C, ticker.Stop
		}
	}
	return &mediaWorker{store: store, processor: processor, owner: owner, now: now, ticks: ticks}
}

// RunOne receives the authoritative source and token from Mongo. Redis hints are
// intentionally absent from this boundary.
func (w *mediaWorker) RunOne(ctx context.Context) (bool, error) {
	if w.store == nil || w.owner == "" {
		return false, fmt.Errorf("invalid media worker")
	}
	claimCtx, claimCancel := context.WithTimeout(ctx, mediaStoreBudget)
	claimed, err := w.store.Claim(claimCtx, w.owner, w.now(), mediaLeaseTTL)
	claimCancel()
	if err != nil || claimed == nil {
		return false, err
	}

	// Neither Process nor the heartbeat can mutate the other caller's lease.
	lease := *claimed
	processLease := lease
	jobCtx, jobCancel := context.WithTimeout(ctx, mediaJobBudget)
	defer jobCancel()
	heartbeatCtx, heartbeatCancel := context.WithCancel(jobCtx)
	tick, stopTick := w.ticks(mediaHeartbeatInterval)
	heartbeatDone := make(chan error, 1)
	go func() {
		defer stopTick()
		for {
			select {
			case <-heartbeatCtx.Done():
				heartbeatDone <- nil
				return
			case _, ok := <-tick:
				if !ok {
					jobCancel()
					heartbeatDone <- errMediaLeaseLost
					return
				}
				renewCtx, renewCancel := context.WithTimeout(heartbeatCtx, mediaStoreBudget)
				matched, renewErr := w.store.Renew(renewCtx, &lease, w.now(), mediaLeaseTTL)
				renewCancel()
				// Stopping this goroutine or shutting down the parent is not a
				// failed renewal. A live renewal timeout/error is conservative loss.
				if heartbeatCtx.Err() != nil {
					heartbeatDone <- nil
					return
				}
				if renewErr != nil || !matched {
					jobCancel()
					heartbeatDone <- fmt.Errorf("%w: %v", errMediaLeaseLost, renewErr)
					return
				}
			}
		}
	}()

	var result *model.MediaResult
	var processErr error
	if w.processor == nil {
		processErr = &mediaProcessError{Code: "internal_error", Retryable: true, Err: errors.New("media processor unavailable")}
	} else {
		result, processErr = w.processor.Process(jobCtx, &processLease)
	}
	heartbeatCancel()
	// No final CAS races an in-flight heartbeat, including during cancellation.
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil {
		w.cleanup(ctx, &lease, result)
		return true, heartbeatErr
	}
	if jobCtx.Err() != nil {
		processErr = jobCtx.Err()
	}
	if processErr != nil {
		w.cleanup(ctx, &lease, result)
		code, retryable := mediaFailure(processErr)
		failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), mediaStoreBudget)
		_, failErr := w.store.Fail(failCtx, &lease, w.now(), code, retryable)
		failCancel()
		return true, errors.Join(processErr, failErr)
	}

	completeCtx, completeCancel := context.WithTimeout(ctx, mediaStoreBudget)
	matched, completeErr := w.store.Complete(completeCtx, &lease, w.now(), result)
	completeCancel()
	if completeErr != nil {
		// Mongo may have committed before a network timeout. Preserve all
		// outputs until reconciliation establishes whether they are referenced.
		return true, completeErr
	}
	if !matched {
		w.cleanup(ctx, &lease, result)
		return true, errMediaLeaseLost
	}
	return true, nil
}

func (w *mediaWorker) cleanup(parent context.Context, lease *model.MediaLease, result *model.MediaResult) {
	if w.processor == nil || result == nil || len(result.Manifest) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), mediaStoreBudget)
	defer cancel()
	if err := w.processor.Cleanup(ctx, lease, result); err != nil {
		log.Printf("cleanup media job %s token %d: %v", lease.JobID, lease.LeaseToken, err)
	}
}

func mediaFailure(err error) (string, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	var processErr *mediaProcessError
	if errors.As(err, &processErr) {
		switch processErr.Code {
		case "source_download_failed", "invalid_media", "transcode_failed", "cover_failed", "upload_failed", "timeout", "lease_lost", "attempts_exhausted", "internal_error":
			return processErr.Code, processErr.Retryable
		}
	}
	return "internal_error", true
}

// Index failures gate recovery. There is no queue recovery/consumption here.
func ensureMongoMediaIndexes(ctx context.Context, store repo.MediaJobStore) error {
	indexCtx, indexCancel := context.WithTimeout(ctx, mediaStoreBudget)
	err := store.EnsureIndexes(indexCtx)
	indexCancel()
	if err != nil {
		return fmt.Errorf("media job index gate: %w", err)
	}
	return nil
}

func runMongoMediaLoop(ctx context.Context, store repo.MediaJobStore, processor mediaProcessor, owner string) error {
	if err := ensureMongoMediaIndexes(ctx, store); err != nil {
		return err
	}
	return runMongoMediaScan(ctx, store, processor, owner)
}

func runMongoMediaScan(ctx context.Context, store repo.MediaJobStore, processor mediaProcessor, owner string) error {
	worker := newMediaWorker(store, processor, owner, nil, nil)
	for ctx.Err() == nil {
		claimed, err := worker.RunOne(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("persistent media task: %v", err)
		}
		if !claimed || err != nil {
			timer := time.NewTimer(mediaScanInterval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			}
		}
	}
	return ctx.Err()
}
