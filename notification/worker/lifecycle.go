package worker

import (
	"context"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"
	"fmt"
	"log/slog"
	"time"
)

type subscriptionReader interface {
	SubscriptionSnapshot(string) rabbitmq.ConsumerSnapshot
}
type contextUnsubscriber interface {
	UnsubscribeContext(context.Context, string) error
}
type stopAttempt struct {
	done chan struct{}
	err  error
}

func normalizeConsumerSnapshot(s rabbitmq.ConsumerSnapshot) rabbitmq.ConsumerSnapshot {
	if !s.Known || (s.Active != 0 && s.Active != 1) {
		return rabbitmq.ConsumerSnapshot{Active: -1}
	}
	return s
}
func (w *Worker) readSubscription(id string) rabbitmq.ConsumerSnapshot {
	reader, ok := w.broker.(subscriptionReader)
	if !ok {
		return rabbitmq.ConsumerSnapshot{Active: -1}
	}
	return normalizeConsumerSnapshot(reader.SubscriptionSnapshot(id))
}
func (w *Worker) consumerSnapshot() rabbitmq.ConsumerSnapshot {
	w.mu.Lock()
	id, busy, uncertain := w.subID, w.starting, w.creationUnknown
	w.mu.Unlock()
	if busy || uncertain {
		return rabbitmq.ConsumerSnapshot{Active: -1}
	}
	if id == "" {
		return rabbitmq.ConsumerSnapshot{Active: 0, Known: true}
	}
	snapshot := w.readSubscription(id)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.subID != id || w.starting || w.creationUnknown {
		return rabbitmq.ConsumerSnapshot{Active: -1}
	}
	if w.stopRequested && snapshot.Active != 0 {
		return rabbitmq.ConsumerSnapshot{Active: -1}
	}
	return snapshot
}

func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.starting || w.creationUnknown || w.subID != "" {
		w.mu.Unlock()
		return fmt.Errorf("notification worker already starting or subscribed")
	}
	w.starting = true
	w.mu.Unlock()
	finish := func(id string, err error) error {
		w.mu.Lock()
		w.starting = false
		w.subID = id
		w.creationUnknown = id == "" && err == nil
		w.stopRequested = id != "" && err != nil
		w.mu.Unlock()
		if id == "" && err == nil {
			return fmt.Errorf("notification subscription result unconfirmed: missing ID")
		}
		return err
	}
	if w.svc == nil || w.broker == nil {
		return finish("", fmt.Errorf("notification worker dependency unavailable"))
	}
	if err := w.svc.EnsureIndexes(ctx); err != nil {
		return finish("", fmt.Errorf("notification indexes: %w", err))
	}
	id, err := w.broker.Subscribe(ctx, queueName, w.handle)
	if err = finish(id, err); err != nil {
		return err
	}
	slog.Info("notification worker started", "sub", id, "queue", queueName)
	snapshot := w.consumerSnapshot()
	slog.Info("local consumer state", "count", snapshot.Active, "known", snapshot.Known, "queue", queueName)
	return nil
}

func (w *Worker) confirmStopped(id string) {
	snapshot := w.readSubscription(id)
	if !snapshot.Known || snapshot.Active != 0 {
		return
	}
	w.mu.Lock()
	if w.subID == id && !w.stopInProgress {
		w.subID = ""
		w.stopRequested = false
		w.legacyStop = nil
	}
	w.mu.Unlock()
}

func (w *Worker) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	w.mu.Lock()
	if w.starting {
		w.mu.Unlock()
		return fmt.Errorf("notification worker busy creating subscription")
	}
	if w.creationUnknown {
		w.mu.Unlock()
		return fmt.Errorf("notification subscription result unconfirmed")
	}
	id := w.subID
	if id == "" {
		w.mu.Unlock()
		return nil
	}
	w.stopRequested = true
	if unsub, ok := w.broker.(contextUnsubscriber); ok {
		if w.stopInProgress {
			w.mu.Unlock()
			return fmt.Errorf("notification worker stop already in progress")
		}
		w.stopInProgress = true
		w.mu.Unlock()
		err := unsub.UnsubscribeContext(ctx, id)
		w.mu.Lock()
		w.stopInProgress = false
		w.mu.Unlock()
		w.confirmStopped(id)
		return err
	}
	// Legacy implementations cannot be forced to cooperate. Share a single pending
	// attempt so caller timeouts do not accumulate blocked goroutines.
	attempt := w.legacyStop
	if attempt != nil {
		select {
		case <-attempt.done:
			if attempt.err != nil {
				attempt = nil
			}
		default:
		}
	}
	if attempt == nil {
		attempt = &stopAttempt{done: make(chan struct{})}
		w.legacyStop = attempt
		go func(a *stopAttempt) {
			err := w.broker.Unsubscribe(id)
			w.mu.Lock()
			a.err = err
			close(a.done)
			w.mu.Unlock()
		}(attempt)
	}
	w.mu.Unlock()
	select {
	case <-attempt.done:
		w.confirmStopped(id)
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
