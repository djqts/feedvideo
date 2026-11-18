package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"feedvideo.local/notification/repo"
	"feedvideo.local/notification/service"
	notifworker "feedvideo.local/notification/worker"
	"feedvideo.local/shared-db/cache"
	mongorepo "feedvideo.local/shared-db/mongo"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb, err := cache.InitRedis(envOr("REDIS_ADDR", "redis:6379"), envOr("REDIS_PASSWORD", ""), 0)
	if err != nil {
		log.Fatalf("redis init: %v", err)
	}
	defer rdb.Client.Close()

	initCtx, initCancel := context.WithTimeout(ctx, 10*time.Second)
	inst, err := mongorepo.Init(initCtx, envOr("MONGO_URI", "mongodb://mongodb:27017"), envOr("MONGO_DB", "feedvideo"))
	initCancel()
	if err != nil {
		log.Fatalf("mongo init: %v", err)
	}
	defer func() { _ = inst.Disconnect(context.Background()) }()

	notifStore := repo.NewMongoStore(inst.Database())
	notifUnread := repo.NewRedisUnread(rdb)
	notifSvc := service.NewService(notifStore, notifUnread)

	brokerCtx, brokerCancel := context.WithTimeout(ctx, 10*time.Second)
	b, err := rabbitmq.NewBroker(brokerCtx, rabbitmq.Config{
		URL:   envOr("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672"),
		VHost: "/",
	})
	brokerCancel()
	if err != nil {
		log.Fatalf("rabbitmq broker: %v", err)
	}
	defer b.Close(context.Background())

	if err := b.DeclareTopology(ctx, "notifications", "notifications_dlq"); err != nil {
		log.Fatalf("declare topology: %v", err)
	}
	w := notifworker.New(b, notifSvc)
	if err := w.Start(ctx); err != nil {
		log.Fatalf("notification worker start: %v", err)
	}
	metricsSrv := w.StartMetrics(envOr("NOTIF_METRICS_ADDR", "127.0.0.1:9092"))
	log.Println("notification worker started (standalone)")

	<-ctx.Done()
	log.Println("shutting down notification worker")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = w.Stop(shutdownCtx)
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
}
