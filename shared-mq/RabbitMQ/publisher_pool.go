package rabbitmq

import (
	"context"
	"fmt"
	"sync"

	amqpclient "github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

// PublisherPool 缓存 *Publisher 按地址复用，避免每次 Publish 都创建新的发布者。
// 底层 AmqpConnection 已内置实体追踪与重连恢复，因此缓存中的 Publisher 在重连后自动恢复。
type PublisherPool struct {
	mu      sync.RWMutex
	cache   map[string]*amqpclient.Publisher
	conn    *amqpclient.AmqpConnection
	closed  bool
}

// NewPublisherPool 创建发送器缓存。
func NewPublisherPool(conn *amqpclient.AmqpConnection) *PublisherPool {
	return &PublisherPool{
		cache: make(map[string]*amqpclient.Publisher),
		conn:  conn,
	}
}

// GetOrCreate 获取或创建指定地址的 Publisher。
// address 格式:
//   - 队列: "/queues/my_queue" 或使用 QueueAddress
//   - 交换器: "/exchanges/my_exchange/routing_key" 或使用 ExchangeAddress
func (p *PublisherPool) GetOrCreate(ctx context.Context, address string) (*amqpclient.Publisher, error) {
	// 读锁快速路径
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, amqpclient.ErrConnectionClosed
	}
	pub, ok := p.cache[address]
	p.mu.RUnlock()
	if ok {
		return pub, nil
	}

	// 写锁创建路径
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, amqpclient.ErrConnectionClosed
	}
	// 双重检查
	if pub, ok = p.cache[address]; ok {
		return pub, nil
	}

	target := parseTargetAddress(address)
	pub, err := p.conn.NewPublisher(ctx, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create publisher for %s: %w", address, err)
	}
	p.cache[address] = pub
	return pub, nil
}

// Remove 从缓存中移除并关闭指定的 Publisher。
func (p *PublisherPool) Remove(ctx context.Context, address string) error {
	p.mu.Lock()
	pub, ok := p.cache[address]
	if ok {
		delete(p.cache, address)
	}
	p.mu.Unlock()

	if ok {
		return pub.Close(ctx)
	}
	return nil
}

// Close 关闭所有缓存的 Publisher。
func (p *PublisherPool) Close(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for addr, pub := range p.cache {
		_ = pub.Close(ctx)
		delete(p.cache, addr)
	}
}

// parseTargetAddress 将地址字符串解析为 ITargetAddress。
// 以 "/queues/" 开头视为队列，以 "/exchanges/" 开头视为交换器。
func parseTargetAddress(address string) amqpclient.ITargetAddress {
	if len(address) > 8 && address[:8] == "/queues/" {
		return &amqpclient.QueueAddress{
			Queue: address[8:],
		}
	}
	// 交换器地址格式: /exchanges/{exchange}/{routing_key}
	if len(address) > 11 && address[:11] == "/exchanges/" {
		rest := address[11:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == '/' {
				return &amqpclient.ExchangeAddress{
					Exchange: rest[:i],
					Key:      rest[i+1:],
				}
			}
		}
		return &amqpclient.ExchangeAddress{
			Exchange: rest,
		}
	}
	// 兜底: 当作队列名处理
	return &amqpclient.QueueAddress{
		Queue: address,
	}
}
