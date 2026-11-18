// Package rabbitmq 提供对 RabbitMQ AMQP 1.0 客户端的生产级业务封装。
//
// 核心能力：
//   - 连接池化与自动重连（利用底层 RecoveryConfiguration）
//   - Publisher 缓存按地址复用
//   - 消费者动态订阅/取消订阅
//   - 消息手动确认（Ack/Nack）
//   - 结构化日志与监控埋点接口
//   - 优雅关闭
//
// 快速使用：
//
//	broker, err := rabbitmq.NewBroker(ctx, rabbitmq.Config{
//	    URL: "amqp://guest:guest@localhost:5672/",
//	})
//	if err != nil {
//	    panic(err)
//	}
//	defer broker.Close(ctx)
//
//	// 发布
//	broker.Publish(ctx, "/queues/my_queue", []byte(`{"hello":"world"}`))
//
//	// 订阅
//	id, _ := broker.Subscribe(ctx, "my_queue", func(msg *rabbitmq.Message) error {
//	    fmt.Println("received:", string(msg.Body))
//	    return msg.Ack()
//	})
//	defer broker.Unsubscribe(id)
package rabbitmq
