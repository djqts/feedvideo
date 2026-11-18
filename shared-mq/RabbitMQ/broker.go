package rabbitmq

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/go-amqp"
	amqpclient "github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

var ErrPublishUnconfirmed = errors.New("rabbitmq publish not accepted")

// Broker 是对 RabbitMQ AMQP 1.0 客户端的业务级封装。
// 提供连接管理、发送器缓存、消费者动态管理、自动重连恢复、优雅关闭等能力。
type Broker struct {
	cfg       Config
	env       *amqpclient.Environment
	conn      *amqpclient.AmqpConnection
	senders   *PublisherPool
	consMgr   *ConsumerManager
	publishFn func(context.Context, string, *amqp.Message) (*amqpclient.PublishResult, error)

	mu     sync.RWMutex
	closed bool
}

// NewBroker 创建 Broker 并建立到 RabbitMQ 的连接。
func NewBroker(ctx context.Context, cfg Config) (*Broker, error) {
	cfg = applyDefaults(cfg)

	env := amqpclient.NewEnvironment(cfg.URL, buildConnOptions(cfg))

	conn, err := env.NewConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial: %w", err)
	}

	b := &Broker{
		cfg:     cfg,
		env:     env,
		conn:    conn,
		senders: NewPublisherPool(conn),
		consMgr: NewConsumerManager(conn, cfg),
	}
	return b, nil
}

// Publish 发送消息到指定地址（队列名或交换器地址）。
// address 格式: "/queues/my_queue" 或 "/exchanges/my_exchange/routing_key"
func (b *Broker) Publish(ctx context.Context, address string, body []byte) error {
	contentType := "application/json"
	return b.PublishWithProperties(ctx, address, body, &amqp.MessageProperties{
		ContentType: &contentType,
	})
}

// PublishWithProperties 发送带自定义属性的消息。
func (b *Broker) PublishWithProperties(ctx context.Context, address string, body []byte, props *amqp.MessageProperties) error {
	start := time.Now()
	defer func() {
		b.cfg.Metrics.ObserveMQPublishLatency(time.Since(start).Seconds())
	}()

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return amqpclient.ErrConnectionClosed
	}
	b.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		b.cfg.Metrics.IncSendFailure()
		return err
	}
	timeout := b.cfg.PublishTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg := amqp.NewMessage(body)
	msg.Header = &amqp.MessageHeader{Durable: true}
	properties := amqp.MessageProperties{}
	if props != nil {
		properties = *props
	}
	if properties.ContentType == nil {
		contentType := "application/json"
		properties.ContentType = &contentType
	}
	if properties.MessageID == nil {
		properties.MessageID = stableMessageID(body)
	}
	msg.Properties = &properties
	result, err := b.publishMessage(ctx, address, msg)
	if err != nil {
		b.cfg.Metrics.IncSendFailure()
		return fmt.Errorf("rabbitmq: publish to %s: %w", address, err)
	}
	if result == nil {
		b.cfg.Metrics.IncSendFailure()
		return fmt.Errorf("%w: to %s: missing result", ErrPublishUnconfirmed, address)
	}
	accepted, ok := result.Outcome.(*amqp.StateAccepted)
	if !ok || accepted == nil {
		b.cfg.Metrics.IncSendFailure()
		return fmt.Errorf("%w: to %s: outcome=%T rejection=%v", ErrPublishUnconfirmed, address, result.Outcome, result.MessageRejectedError)
	}
	b.cfg.Metrics.IncSendSuccess()
	return nil
}

func stableMessageID(body []byte) string {
	var event struct {
		EventID string `json:"event_id"`
	}
	if json.Unmarshal(body, &event) == nil && event.EventID != "" {
		return event.EventID
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body))
}

func (b *Broker) publishMessage(ctx context.Context, address string, msg *amqp.Message) (*amqpclient.PublishResult, error) {
	if b.publishFn != nil {
		return b.publishFn(ctx, address, msg)
	}
	pub, err := b.senders.GetOrCreate(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("get publisher for %s: %w", address, err)
	}
	return pub.Publish(ctx, msg)
}

// Subscribe 订阅队列消息。返回 subscriptionID 用于后续取消订阅。
func (b *Broker) Subscribe(ctx context.Context, queueName string, handler MessageHandler) (string, error) {
	return b.consMgr.Subscribe(ctx, queueName, handler)
}

// Unsubscribe 取消订阅。
func (b *Broker) Unsubscribe(id string) error {
	return b.consMgr.Unsubscribe(id)
}

func (b *Broker) UnsubscribeContext(ctx context.Context, id string) error {
	if b == nil {
		return fmt.Errorf("rabbitmq broker is nil")
	}
	return b.consMgr.UnsubscribeContext(ctx, id)
}

func (b *Broker) SubscriptionSnapshot(id string) ConsumerSnapshot {
	if b == nil {
		return ConsumerSnapshot{Active: -1}
	}
	return b.consMgr.SubscriptionSnapshot(id)
}

// Ping 校验 Broker 连通性（用于 /health 探测）。
func (b *Broker) Ping(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("rabbitmq broker is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed || b.conn == nil {
		return fmt.Errorf("rabbitmq broker unavailable")
	}
	if _, open := b.conn.State().(*amqpclient.StateOpen); !open {
		return fmt.Errorf("rabbitmq broker unavailable")
	}
	return nil
}

// RequeueMessages moves up to max messages from one queue to another
// (used to replay dead-lettered notifications). Returns the number requeued.
func (b *Broker) RequeueMessages(ctx context.Context, fromQueue, toQueue string, max int) (int, error) {
	if max <= 0 {
		max = 100
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return 0, amqpclient.ErrConnectionClosed
	}
	b.mu.RUnlock()

	consumer, err := b.conn.NewConsumer(ctx, fromQueue, nil)
	if err != nil {
		return 0, fmt.Errorf("rabbitmq: consume %s: %w", fromQueue, err)
	}
	defer consumer.Close(context.Background())

	requeued := 0
	for requeued < max {
		recvCtx, recvCancel := context.WithTimeout(ctx, 2*time.Second)
		dc, err := consumer.Receive(recvCtx)
		recvCancel()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return requeued, fmt.Errorf("rabbitmq: receive from %s: %w", fromQueue, err)
		}
		raw := dc.Message()
		if err := b.PublishWithProperties(ctx, "/queues/"+toQueue, raw.GetData(), raw.Properties); err != nil {
			return requeued, err
		}
		if err := dc.Accept(ctx); err != nil {
			return requeued, fmt.Errorf("rabbitmq: accept dlq message: %w", err)
		}
		requeued++
	}
	return requeued, nil
}

// Close 优雅关闭 Broker，等待所有消费者退出后释放资源。
func (b *Broker) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	b.consMgr.Close()
	b.senders.Close(ctx)
	return b.env.CloseConnections(ctx)
}

// applyDefaults 填充配置默认值。
func applyDefaults(cfg Config) Config {
	if cfg.PrefetchCount == 0 {
		cfg.PrefetchCount = 10
	}
	if cfg.PublishTimeout == 0 {
		cfg.PublishTimeout = 10 * time.Second
	}
	if cfg.ConsumeTimeout == 0 {
		cfg.ConsumeTimeout = 0 // 无限等待
	}
	if cfg.MaxReconnectAttempts == 0 {
		cfg.MaxReconnectAttempts = 10
	}
	if cfg.Logger == nil {
		cfg.Logger = &noopLogger{}
	}
	if cfg.Metrics == nil {
		cfg.Metrics = &noopMetrics{}
	}
	return cfg
}

// buildConnOptions 根据 Config 构造底层连接选项。
func buildConnOptions(cfg Config) *amqpclient.AmqpConnOptions {
	opts := &amqpclient.AmqpConnOptions{
		SASLType: amqp.SASLTypeAnonymous(),
		RecoveryConfiguration: &amqpclient.RecoveryConfiguration{
			ActiveRecovery:           true,
			MaxReconnectAttempts:     cfg.MaxReconnectAttempts,
			BackOffReconnectInterval: 2 * time.Second,
		},
		TopologyRecoveryOptions: amqpclient.TopologyRecoveryAllEnabled,
	}
	if cfg.Heartbeat > 0 {
		opts.IdleTimeout = cfg.Heartbeat
	}
	return opts
}

// --- 默认实现 ---

type noopLogger struct{}

func (l *noopLogger) Info(msg string, keysAndValues ...any)             {}
func (l *noopLogger) Error(err error, msg string, keysAndValues ...any) {}

type noopMetrics struct{}

func (m *noopMetrics) IncSendSuccess()                         {}
func (m *noopMetrics) IncSendFailure()                         {}
func (m *noopMetrics) IncReceiveCount()                        {}
func (m *noopMetrics) SetConsumerGauge(count int)              {}
func (m *noopMetrics) ObserveMQPublishLatency(seconds float64) {}
func (m *noopMetrics) ObserveMQConsumeLatency(seconds float64) {}
