package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log"

	amqpclient "github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

// DeclareTopology pre-declares the required queues on the broker.
// Declaration is idempotent and safe to call on every startup.
//
// This must run before any publisher or consumer attaches: the AMQP 1.0
// client does NOT auto-create missing queues, and publishing to a missing
// queue fails with amqp:not-found while consumers fail silently.
func (b *Broker) DeclareTopology(ctx context.Context, queues ...string) error {
	if b == nil {
		return errors.New("rabbitmq: broker is nil")
	}
	if b.conn == nil {
		return errors.New("rabbitmq: broker connection is nil")
	}
	for _, queue := range queues {
		if queue == "" {
			continue
		}
		if _, err := b.conn.Management().DeclareQueue(ctx, &amqpclient.DefaultQueueSpecification{
			Name: queue,
		}); err != nil {
			return fmt.Errorf("rabbitmq: declare queue %q: %w", queue, err)
		}
		log.Printf("[MQ] queue %q declared (durable)", queue)
	}
	return nil
}
