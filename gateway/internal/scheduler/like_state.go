package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"github.com/google/uuid"
)

type likeStateNotifier interface {
	Publish(context.Context, events.Event) error
}
type likeStateCursors struct {
	pending, full, relayAfter string
	batch                     *likeProjectionBatch
	relay                     *likeEventProgress
}

// A retained page contains at most 99 relations. Only its unstarted tail survives a pass.
type likeProjectionBatch struct {
	pending, full, rows           []*model.LikeState
	pendingLimit, fullLimit, next int
	attempted                     map[string]bool
}

// Notification progress is independent of projection pages and bounded by one durable relation's 128 intents.
type likeEventProgress struct {
	state *model.LikeState
	next  int
}

func RunLikeStateLoop(ctx context.Context, store repo.LikeStateStore, projection repo.LikeProjectionStore, notifier likeStateNotifier) {
	if store == nil || projection == nil {
		return
	}
	owner := uuid.NewString()
	cursors := &likeStateCursors{}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := runLikeStatePass(ctx, store, projection, notifier, owner, cursors); err != nil && ctx.Err() == nil {
			slog.Warn("like state projection pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runLikeStatePass(ctx context.Context, store repo.LikeStateStore, projection repo.LikeProjectionStore, notifier likeStateNotifier, owner string, cursors *likeStateCursors) (passErr error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	lease, err := projection.Claim(ctx, owner, time.Now(), 30*time.Second)
	if err != nil || lease == nil {
		return err
	}
	defer func() { passErr = errors.Join(passErr, projection.Release(ctx, lease)) }()
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	workCtx, workCancel := context.WithDeadline(ctx, time.Now().Add(remaining*3/4))
	if cursors.batch == nil {
		cursors.batch, err = loadLikeProjectionBatch(workCtx, store, cursors)
	}
	if err == nil {
		err = projectLikeBatch(workCtx, store, projection, lease, cursors)
	}
	workCancel()
	passErr = errors.Join(passErr, err)
	// A slow old relay never holds the projection scan cursor or a completed projection page.
	if notifier != nil {
		relayCtx, relayCancel := context.WithTimeout(ctx, remaining/4)
		passErr = errors.Join(passErr, relayLikeEvents(relayCtx, store, notifier, cursors))
		relayCancel()
	}
	return passErr
}

func loadLikeProjectionBatch(ctx context.Context, store repo.LikeStateStore, cursors *likeStateCursors) (*likeProjectionBatch, error) {
	pending, err := store.ListLikeStates(ctx, cursors.pending, 49, true)
	if err != nil {
		return nil, err
	}
	if len(pending) > 49 {
		return nil, errors.New("like pending scan exceeded batch limit")
	}
	fullLimit := 99 - len(pending)
	full, err := store.ListLikeStates(ctx, cursors.full, fullLimit, false)
	if err != nil {
		return nil, err
	}
	if len(full) > fullLimit {
		return nil, errors.New("like full scan exceeded batch limit")
	}
	batch := &likeProjectionBatch{pending: pending, full: full, pendingLimit: 49, fullLimit: fullLimit, attempted: map[string]bool{}}
	latest := map[string]*model.LikeState{}
	for _, row := range append(pending, full...) {
		if row == nil {
			return nil, errors.New("invalid nil like scan result")
		}
		if old := latest[row.ID]; old == nil || row.Version >= old.Version {
			latest[row.ID] = row
		}
	}
	queued := map[string]bool{}
	for i := 0; i < len(pending) || i < len(full); i++ {
		// Interleaving keeps full work advancing under a continuous dirty backlog.
		for _, page := range [][]*model.LikeState{pending, full} {
			if i < len(page) {
				row := page[i]
				if !queued[row.ID] {
					batch.rows = append(batch.rows, latest[row.ID])
					queued[row.ID] = true
				}
			}
		}
	}
	advanceLikeProcessedCursor(&cursors.pending, pending, 49, batch.attempted)
	advanceLikeProcessedCursor(&cursors.full, full, fullLimit, batch.attempted)
	return batch, nil
}

func projectLikeBatch(ctx context.Context, store repo.LikeStateStore, projection repo.LikeProjectionStore, lease *repo.LikeProjectionLease, cursors *likeStateCursors) (passErr error) {
	batch := cursors.batch
	if batch == nil {
		return nil
	}
	// All retained versions were read before this token's first CountActive for their target.
	targets := map[string]bool{}
	for batch.next < len(batch.rows) {
		if err := ctx.Err(); err != nil {
			return errors.Join(passErr, err)
		}
		state := batch.rows[batch.next]
		key := state.TargetType + ":" + state.TargetID
		countOK, seen := targets[key]
		if !seen {
			count, err := store.CountActive(ctx, state.TargetID, state.TargetType)
			if err == nil {
				countOK, err = projection.ProjectTarget(ctx, lease, state.TargetID, state.TargetType, count, time.Now())
			}
			if err != nil {
				countOK = false
			}
			passErr = errors.Join(passErr, err)
			targets[key] = countOK
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(passErr, err)
		}
		err := projection.ProjectMembership(ctx, state)
		// Failure is retryable durable work; an attempted row must not permanently block the page tail.
		batch.next++
		batch.attempted[state.ID] = true
		advanceLikeProcessedCursor(&cursors.pending, batch.pending, batch.pendingLimit, batch.attempted)
		advanceLikeProcessedCursor(&cursors.full, batch.full, batch.fullLimit, batch.attempted)
		passErr = errors.Join(passErr, err)
		if err == nil && countOK && state.PendingProjection {
			_, err = store.ClearLikeProjection(ctx, state.ID, state.Version)
			passErr = errors.Join(passErr, err)
		}
	}
	cursors.batch = nil
	return passErr
}

func advanceLikeProcessedCursor(cursor *string, rows []*model.LikeState, limit int, attempted map[string]bool) {
	processed := 0
	for _, row := range rows {
		if !attempted[row.ID] {
			break
		}
		*cursor = row.ID
		processed++
	}
	if processed == len(rows) && len(rows) < limit {
		*cursor = ""
	}
}

func relayLikeEvents(ctx context.Context, store repo.LikeStateStore, notifier likeStateNotifier, cursors *likeStateCursors) (relayErr error) {
	if cursors.relay == nil {
		rows, err := store.ListLikeStates(ctx, cursors.relayAfter, 1, true)
		if err != nil {
			return err
		}
		if len(rows) == 0 && cursors.relayAfter != "" {
			cursors.relayAfter = ""
			rows, err = store.ListLikeStates(ctx, "", 1, true)
			if err != nil {
				return err
			}
		}
		if len(rows) > 1 {
			return errors.New("like relay scan exceeded batch limit")
		}
		if len(rows) == 0 {
			return nil
		}
		if rows[0] == nil || len(rows[0].PendingEvents) > 128 {
			return errors.New("invalid like relay snapshot")
		}
		cursors.relay = &likeEventProgress{state: rows[0]}
	}
	progress := cursors.relay
	for progress.next < len(progress.state.PendingEvents) {
		if err := ctx.Err(); err != nil {
			return errors.Join(relayErr, err)
		}
		intent := progress.state.PendingEvents[progress.next]
		// Advance only this bounded retry position. The original durable failed intent remains untouched.
		progress.next++
		if intent.EventID == "" || intent.Event.EventID != intent.EventID {
			relayErr = errors.Join(relayErr, errors.New("invalid durable like event identity"))
			continue
		}
		publishCtx, publishCancel := context.WithTimeout(ctx, 5*time.Second)
		err := notifier.Publish(publishCtx, intent.Event)
		publishCancel()
		if err == nil {
			err = store.AckLikeEvent(ctx, progress.state.ID, intent.EventID)
		}
		relayErr = errors.Join(relayErr, err)
	}
	cursors.relayAfter = progress.state.ID
	cursors.relay = nil
	return relayErr
}
