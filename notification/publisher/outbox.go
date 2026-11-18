package publisher

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"feedvideo.local/shared-db/outbox"
	events "feedvideo.local/shared-events"
)

// OutboxWriter 最小 Outbox 写入接口（由 shared-db/outbox.Store 实现）。
type OutboxWriter interface {
	Insert(ctx context.Context, evts ...outbox.Event) error
}

// OutboxNotifier 把业务事件先写入 Outbox，由独立 relay 幂等投递到 MQ（P3.2）。
// 相比直接 Publish，Broker 故障不会丢失事件；重复投递由 event_id 去重兜底。
type OutboxNotifier struct {
	store OutboxWriter
}

func NewOutboxNotifier(store OutboxWriter) *OutboxNotifier {
	return &OutboxNotifier{store: store}
}

func (o *OutboxNotifier) Publish(ctx context.Context, evt events.Event) error {
	if evt.EventID == "" {
		evt.EventID = newEventID()
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal outbox event: %w", err)
	}
	return o.store.Insert(ctx, outbox.Event{
		EventType:     string(evt.Type),
		EventID:       evt.EventID,
		AggregateType: evt.TargetType,
		AggregateID:   evt.TargetID,
		Payload:       payload,
	})
}

func newEventID() string {
	b := make([]byte, 16)
	// crypto/rand 失败时退化为时间戳+纳秒（仍保证进程内唯一）。
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("evt-%x", b)
}
