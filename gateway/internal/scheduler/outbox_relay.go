package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"feedvideo.local/shared-db/cache"
	"feedvideo.local/shared-db/outbox"
	events "feedvideo.local/shared-events"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"
)

type outboxRelayStore interface {
	ClaimLease(context.Context, int, string, int) ([]outbox.Record, error)
	MarkPublishedLease(context.Context, outbox.Record) error
	RecordFailureLease(context.Context, outbox.Record) error
	PendingStats(context.Context) (int64, int64, error)
}

type outboxPublisher interface {
	Publish(context.Context, string, []byte) error
}

// RunOutboxRelayLoop 定期把 Outbox pending 事件投递到 RabbitMQ（P3.2）。
// - 投递成功 → 标记 published；
// - 投递失败 → 保留 pending 并 attempts+1，下轮重试；
// - broker 为 nil（MQ 降级）→ 保持 pending 不丢，输出告警日志。
func RunOutboxRelayLoop(ctx context.Context, store *outbox.Store, broker *rabbitmq.Broker, rdb *cache.RedisClient, interval time.Duration) {
	if broker == nil {
		return
	}
	runOutboxRelayLoop(ctx, store, broker, rdb, interval)
}

func runOutboxRelayLoop(ctx context.Context, store outboxRelayStore, broker outboxPublisher, rdb *cache.RedisClient, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	host, _ := os.Hostname()
	owner := fmt.Sprintf("%s-%d", host, os.Getpid())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	relayOnce := func() {
		if broker == nil {
			return
		}
		// 短事务领取租约（30s）；发布不持数据库锁，崩溃后租约过期可重领。
		rows, err := store.ClaimLease(ctx, 1, owner, 30)
		if err != nil {
			log.Printf("[outbox] claim lease: %v", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, r := range rows {
			payload, err := outboxNotificationPayload(r)
			if err == nil && payload != nil {
				pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = broker.Publish(pctx, "/queues/notifications", payload)
				cancel()
			}
			if err != nil {
				log.Printf("[outbox] publish event %d (%s) failed: %v", r.ID, r.EventType, err)
				if err := store.RecordFailureLease(ctx, r); err != nil {
					log.Printf("[outbox] record failure event %d: %v", r.ID, err)
				}
				continue
			}
			if err := store.MarkPublishedLease(ctx, r); err != nil {
				log.Printf("[outbox] mark published event %d: %v", r.ID, err)
			}
		}
		// 指标：pending 数量与最老年龄（gateway /metrics 经 statReader 读取）
		if rdb != nil {
			if cnt, age, err := store.PendingStats(ctx); err == nil {
				_ = rdb.Set(ctx, "metrics:outbox:pending", cnt, 60*time.Second)
				_ = rdb.Set(ctx, "metrics:outbox:oldest_age", age, 60*time.Second)
			}
		}
	}

	// 启动后立即尝试一轮，便于测试与故障恢复。
	relayOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			relayOnce()
		}
	}
}

func outboxNotificationPayload(r outbox.Record) ([]byte, error) {
	if r.EventType == events.RelationChangedType {
		var envelope events.RelationChangedEvent
		if err := json.Unmarshal([]byte(r.Payload), &envelope); err != nil {
			return nil, err
		}
		if envelope.EventID != r.EventID {
			return nil, fmt.Errorf("relation event identity mismatch")
		}
		evt, err := events.DecodeRelationNotification([]byte(r.Payload))
		if err != nil || evt == nil {
			return nil, err
		}
		return json.Marshal(evt)
	}
	switch events.NotifType(r.EventType) {
	case events.NotifLike, events.NotifComment, events.NotifFollow, events.NotifSystem:
	default:
		return nil, fmt.Errorf("unsupported outbox type %q", r.EventType)
	}
	var evt events.Event
	if err := json.Unmarshal([]byte(r.Payload), &evt); err != nil {
		return nil, err
	}
	if evt.Type != events.NotifType(r.EventType) || evt.RecipientID == "" || evt.CreatedAt <= 0 {
		return nil, fmt.Errorf("invalid notification payload")
	}
	if evt.EventID == "" {
		evt.EventID = r.EventID
	}
	if evt.EventID == "" || evt.EventID != r.EventID {
		return nil, fmt.Errorf("notification identity mismatch")
	}
	if evt.Type != events.NotifSystem && (evt.ActorID == "" || evt.TargetID == "" || evt.TargetType == "") {
		return nil, fmt.Errorf("invalid notification target")
	}
	return json.Marshal(evt)
}
