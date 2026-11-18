package publisher

import (
	"context"
	"encoding/json"
	"fmt"

	events "feedvideo.local/shared-events"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"
)

// RabbitPublisher 把 events.Event 序列化为 JSON 并投递到指定队列地址。
type RabbitPublisher struct {
	broker  *rabbitmq.Broker
	address string // 如 "/queues/notifications"
}

func NewRabbitPublisher(broker *rabbitmq.Broker, address string) *RabbitPublisher {
	return &RabbitPublisher{broker: broker, address: address}
}

func (p *RabbitPublisher) Publish(ctx context.Context, evt events.Event) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.broker.Publish(ctx, p.address, body)
}

// NoopPublisher Broker 不可用时的降级发布器：吞掉一切，永不报错。
type NoopPublisher struct{}

func (NoopPublisher) Publish(ctx context.Context, evt events.Event) error { return nil }
