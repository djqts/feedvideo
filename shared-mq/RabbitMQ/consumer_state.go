package rabbitmq

import (
	"context"
	"fmt"
	amqpclient "github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
	"time"
)

// ConsumerSnapshot describes one locally owned receive chain, not a broker-wide count.
// Known=false is always represented by Active=-1.
type ConsumerSnapshot struct {
	Active int
	Known  bool
}

type consumerReceiver interface {
	Receive(context.Context) (amqpclient.IDeliveryContext, error)
	Close(context.Context) error
}
type consumerRuntime interface {
	Create(context.Context, string) (consumerReceiver, error)
	Open() bool
	Now() time.Time
}
type nativeConsumerRuntime struct {
	conn *amqpclient.AmqpConnection
	cfg  Config
}

func (r *nativeConsumerRuntime) Create(ctx context.Context, queue string) (consumerReceiver, error) {
	if r.conn == nil {
		return nil, amqpclient.ErrConnectionClosed
	}
	var opts amqpclient.IConsumerOptions
	if r.cfg.PrefetchCount > 0 {
		opts = &amqpclient.ConsumerOptions{InitialCredits: int32(r.cfg.PrefetchCount)}
	}
	receiver, err := r.conn.NewConsumer(ctx, queue, opts)
	if receiver == nil {
		return nil, err
	}
	return receiver, err
}
func (r *nativeConsumerRuntime) Open() bool {
	if r.conn == nil {
		return false
	}
	_, open := r.conn.State().(*amqpclient.StateOpen)
	return open
}
func (*nativeConsumerRuntime) Now() time.Time { return time.Now() }

func (c *ConsumerManager) SubscriptionSnapshot(id string) ConsumerSnapshot {
	if c == nil || c.runtime == nil || id == "" {
		return ConsumerSnapshot{Active: -1}
	}
	open, now := c.runtime.Open(), c.runtime.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	ac, ok := c.consumers[id]
	if !ok {
		return ConsumerSnapshot{Active: 0, Known: true}
	}
	return c.snapshotLocked(ac, open, now)
}
func (c *ConsumerManager) snapshotLocked(ac *activeConsumer, open bool, now time.Time) ConsumerSnapshot {
	if !open || ac.stopping || ac.completed || ac.ctx == nil || ac.ctx.Err() != nil ||
		!ac.evidence || now.Before(ac.lastReceive) || now.Sub(ac.lastReceive) > 3*time.Second {
		ac.evidence = false
		return ConsumerSnapshot{Active: -1}
	}
	return ConsumerSnapshot{Active: 1, Known: true}
}
func (c *ConsumerManager) recordReceiveEvidence(ac *activeConsumer) {
	c.mu.Lock()
	// State sampling and proof publication form one transaction with snapshot
	// invalidation: an older receive cannot overwrite an already observed fault.
	open, now := c.runtime.Open(), c.runtime.Now()
	if open && !ac.stopping && ac.ctx.Err() == nil {
		ac.evidence = true
		ac.lastReceive = now
	} else {
		ac.evidence = false
	}
	c.mu.Unlock()
	c.refreshConsumerGauge()
}
func (c *ConsumerManager) invalidateReceiveEvidence(ac *activeConsumer) {
	c.mu.Lock()
	ac.evidence = false
	c.mu.Unlock()
	c.refreshConsumerGauge()
}
func (c *ConsumerManager) refreshConsumerGauge() {
	// Serialize publication; callbacks may read snapshots, so never hold the state lock.
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	open, now := c.runtime.Open(), c.runtime.Now()
	c.mu.Lock()
	count := 0
	known := true
	for _, ac := range c.consumers {
		snap := c.snapshotLocked(ac, open, now)
		if !snap.Known {
			known = false
		} else {
			count += snap.Active
		}
	}
	c.mu.Unlock()
	if !known {
		count = -1
	}
	c.cfg.Metrics.SetConsumerGauge(count)
}

func (c *ConsumerManager) UnsubscribeContext(ctx context.Context, id string) error {
	if c == nil {
		return fmt.Errorf("rabbitmq: consumer manager unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c.mu.Lock()
	ac, ok := c.consumers[id]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	ac.stopping, ac.evidence = true, false
	ac.cancel()
	c.mu.Unlock()
	c.refreshConsumerGauge()
	select {
	case <-ac.done:
		c.mu.Lock()
		err := ac.closeErr
		c.mu.Unlock()
		if err == nil {
			c.cfg.Logger.Info("consumer unsubscribed", "id", id)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
