package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/go-amqp"
	amqpclient "github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

// ConsumerManager 管理所有消费者的生命周期：订阅、取消订阅、优雅关闭。
// 底层 AmqpConnection 已内置消费者实体追踪与重连恢复（restartEntities），
// 因此重连后 Consumer 的底层 Receiver 会自动重建，本层仅需在 Receive 出错时等待恢复。
type ConsumerManager struct {
	mu                                         sync.Mutex
	metricsMu                                  sync.Mutex
	consumers                                  map[string]*activeConsumer
	conn                                       *amqpclient.AmqpConnection
	cfg                                        Config
	done                                       bool
	nextID                                     uint64
	runtime                                    consumerRuntime
	receiveTimeout, cleanupTimeout, retryDelay time.Duration
}

type activeConsumer struct {
	queueName   string
	handler     MessageHandler
	cancel      context.CancelFunc
	done        chan struct{}
	ctx         context.Context
	stopping    bool
	completed   bool
	closeErr    error
	evidence    bool
	lastReceive time.Time
}

// NewConsumerManager 创建消费者管理器。
func NewConsumerManager(conn *amqpclient.AmqpConnection, cfg Config) *ConsumerManager {
	cfg = applyDefaults(cfg)
	return &ConsumerManager{
		consumers:      make(map[string]*activeConsumer),
		conn:           conn,
		cfg:            cfg,
		runtime:        &nativeConsumerRuntime{conn: conn, cfg: cfg},
		receiveTimeout: time.Second, cleanupTimeout: 5 * time.Second, retryDelay: time.Second,
	}
}

// Subscribe 订阅队列，消息到达时回调 handler。
// 返回 subscriptionID 用于取消订阅。
func (c *ConsumerManager) Subscribe(ctx context.Context, queueName string, handler MessageHandler) (string, error) {
	c.mu.Lock()
	if c.done {
		c.mu.Unlock()
		return "", amqpclient.ErrConnectionClosed
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return "", err
	}
	c.nextID++
	id := fmt.Sprintf("sub-%s-%d", queueName, c.nextID)
	ctx, cancel := context.WithCancel(ctx)
	ac := &activeConsumer{
		queueName: queueName,
		handler:   handler,
		cancel:    cancel,
		done:      make(chan struct{}),
		ctx:       ctx,
	}
	c.consumers[id] = ac
	c.mu.Unlock()
	c.refreshConsumerGauge()
	// Preserve synchronous creation errors without blocking snapshots or Close.
	consumer, err := c.runtime.Create(ctx, queueName)
	if consumer == nil {
		cancel()
		if err == nil {
			err = errors.New("rabbitmq: consumer creation returned no receiver")
		}
		c.mu.Lock()
		delete(c.consumers, id)
		c.mu.Unlock()
		close(ac.done)
		c.refreshConsumerGauge()
		return "", err
	}
	if err != nil {
		cancel()
	}
	go c.consumeLoop(ctx, id, ac, consumer)
	if err != nil {
		return id, err
	}
	if err := ctx.Err(); err != nil {
		return id, err
	}
	c.cfg.Logger.Info("consumer subscribed", "id", id, "queue", queueName)
	return id, nil
}

// Unsubscribe 取消订阅并等待消费 goroutine 退出。
func (c *ConsumerManager) Unsubscribe(id string) error {
	return c.UnsubscribeContext(context.Background(), id)
}

// Close 关闭所有消费者。
func (c *ConsumerManager) Close() {
	c.mu.Lock()
	c.done = true
	for _, ac := range c.consumers {
		ac.stopping = true
		ac.evidence = false
		ac.cancel()
	}
	c.mu.Unlock()
	c.refreshConsumerGauge()
	// 注意：goroutine 将通过 ctx.Done() 退出，我们这里不等待，避免阻塞关闭序列。
	// Broker.Close() 会先关闭消费者再关闭连接。
}

// consumeLoop 为单个订阅运行的消费循环。
// 创建 Consumer 后持续 Receive；错误时等待底层自动恢复后重试。
func (c *ConsumerManager) consumeLoop(ctx context.Context, id string, ac *activeConsumer, consumer consumerReceiver) {
	defer func() {
		ac.cancel()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), c.cleanupTimeout)
		err := consumer.Close(cleanupCtx)
		cancel()
		c.mu.Lock()
		ac.completed, ac.closeErr, ac.evidence = true, err, false
		if err == nil {
			delete(c.consumers, id)
		}
		c.mu.Unlock()
		close(ac.done)
		c.refreshConsumerGauge()
		if err != nil {
			c.cfg.Logger.Error(err, "consumer close unconfirmed", "id", id)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		receiveCtx, receiveCancel := context.WithTimeout(ctx, c.receiveTimeout)
		dc, err := consumer.Receive(receiveCtx)
		idle := errors.Is(err, context.DeadlineExceeded) && errors.Is(receiveCtx.Err(), context.DeadlineExceeded)
		receiveCancel()
		if ctx.Err() != nil {
			return
		}
		if idle {
			c.recordReceiveEvidence(ac)
			continue
		}
		if err == nil && (dc == nil || dc.Message() == nil) {
			err = errors.New("rabbitmq: receive returned no delivery")
		}
		if err != nil {
			c.invalidateReceiveEvidence(ac)
			// 连接断开或其它错误，等待底层重连恢复后继续
			c.cfg.Logger.Error(err, "receive error, waiting for recovery", "id", id, "queue", ac.queueName)
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.retryDelay):
			}
			continue
		}
		c.recordReceiveEvidence(ac)
		c.cfg.Metrics.IncReceiveCount()
		start := time.Now()
		c.dispatchContext(ctx, id, dc, ac.handler)
		c.cfg.Metrics.ObserveMQConsumeLatency(time.Since(start).Seconds())
	}
}

func (c *ConsumerManager) dispatch(id string, dc amqpclient.IDeliveryContext, handler MessageHandler) {
	c.dispatchContext(context.Background(), id, dc, handler)
}

func (c *ConsumerManager) dispatchContext(ctx context.Context, id string, dc amqpclient.IDeliveryContext, handler MessageHandler) {
	raw := dc.Message()
	msg := &Message{
		Body:          raw.GetData(),
		ContentType:   getContentType(raw),
		MessageID:     getMessageID(raw),
		DeliveryCount: getDeliveryCount(raw),
		dc:            dc,
		ctx:           ctx,
	}

	if err := handler(msg); err != nil {
		c.cfg.Logger.Error(err, "handler error", "id", id)
		if !msg.Settled() {
			if err := msg.Nack(true); err != nil {
				c.cfg.Logger.Error(err, "requeue handler failure", "id", id)
			}
		}
	}
}

func getContentType(msg *amqp.Message) string {
	if msg.Properties != nil && msg.Properties.ContentType != nil {
		return *msg.Properties.ContentType
	}
	return ""
}

func getMessageID(msg *amqp.Message) any {
	if msg.Properties != nil {
		return msg.Properties.MessageID
	}
	return nil
}

func getDeliveryCount(msg *amqp.Message) uint32 {
	if msg.Header != nil {
		return msg.Header.DeliveryCount
	}
	return 0
}
