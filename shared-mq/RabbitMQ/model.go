package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/go-amqp"
)

// Config —— 业务层配置（映射到底层 AmqpConnOptions）
type Config struct {
	URL           string
	VHost         string
	Heartbeat     time.Duration
	PrefetchCount int
	// 发送/接收超时
	PublishTimeout time.Duration
	ConsumeTimeout time.Duration
	// 重连配置
	MaxReconnectAttempts int
	// 日志接口
	Logger Logger
	// 监控注册器
	Metrics Metrics
}

// Logger 结构化日志接口
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Error(err error, msg string, keysAndValues ...any)
}

// Metrics 监控埋点接口
type Metrics interface {
	IncSendSuccess()
	IncSendFailure()
	IncReceiveCount()
	SetConsumerGauge(count int)
	// ObserveMQPublishLatency 记录一次发布耗时（秒）。
	ObserveMQPublishLatency(seconds float64)
	// ObserveMQConsumeLatency 记录一次消费处理耗时（秒）。
	ObserveMQConsumeLatency(seconds float64)
}

// Message 业务消息，封装底层 amqp.Message 与确认操作
type Message struct {
	Body          []byte
	ContentType   string
	MessageID     any
	DeliveryCount uint32

	dc            Settlement
	ctx           context.Context
	once          sync.Once
	settled       atomic.Bool
	settlementErr error
}

// Settlement is the narrow transport disposition boundary.
type Settlement interface {
	Accept(context.Context) error
	Requeue(context.Context) error
	Discard(context.Context, *amqp.Error) error
}

func NewMessage(ctx context.Context, body []byte, settlement Settlement) *Message {
	return &Message{Body: body, dc: settlement, ctx: ctx}
}

// Settled reports whether a disposition was attempted, including ambiguous failures.
func (m *Message) Settled() bool { return m.settled.Load() }

func (m *Message) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m *Message) settle(action func(context.Context) error) error {
	m.once.Do(func() {
		m.settled.Store(true)
		if m.dc == nil {
			m.settlementErr = fmt.Errorf("rabbitmq: missing settlement")
			return
		}
		base := m.ctx
		if base == nil {
			base = context.Background()
		}
		ctx, cancel := context.WithTimeout(base, 5*time.Second)
		defer cancel()
		if err := ctx.Err(); err != nil {
			m.settlementErr = err
			return
		}
		m.settlementErr = action(ctx)
	})
	return m.settlementErr
}

// Ack 确认消息。
func (m *Message) Ack() error {
	return m.settle(func(ctx context.Context) error { return m.dc.Accept(ctx) })
}

// Nack 拒绝消息（requeue=true 则重新入队）。
func (m *Message) Nack(requeue bool) error {
	return m.settle(func(ctx context.Context) error {
		if requeue {
			return m.dc.Requeue(ctx)
		}
		return m.dc.Discard(ctx, &amqp.Error{
			Condition:   "amqp:rejected",
			Description: "rejected by business handler",
		})
	})
}

// MessageHandler 业务消息处理函数
type MessageHandler func(msg *Message) error
