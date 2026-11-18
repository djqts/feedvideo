package worker

import (
	"context"
	"encoding/json"
	"errors"
	"feedvideo.local/notification/model"
	"fmt"
	"github.com/Azure/go-amqp"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	events "feedvideo.local/shared-events"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"
)

const (
	queueName    = "notifications"
	dlqName      = "notifications_dlq"
	maxDelivery  = 3
	storeTimeout = 5 * time.Second
)

type Worker struct {
	broker                                                   Broker
	svc                                                      EventStore
	subID                                                    string
	mu                                                       sync.Mutex
	starting, creationUnknown, stopRequested, stopInProgress bool
	legacyStop                                               *stopAttempt

	// 进程内 Prometheus 文本指标（独立 /metrics 端口，P0-1）
	received  atomic.Int64
	ack       atomic.Int64
	requeue   atomic.Int64
	dlq       atomic.Int64
	malformed atomic.Int64
}

type Broker interface {
	Subscribe(context.Context, string, rabbitmq.MessageHandler) (string, error)
	Unsubscribe(string) error
	// PublishWithProperties returns nil only after broker accepted the delivery.
	PublishWithProperties(context.Context, string, []byte, *amqp.MessageProperties) error
}

type EventStore interface {
	Store(context.Context, events.Event) error
	EnsureIndexes(context.Context) error
}

func New(broker Broker, svc EventStore) *Worker {
	return &Worker{broker: broker, svc: svc}
}

func (w *Worker) handle(msg *rabbitmq.Message) error {
	w.received.Add(1)
	var evt events.Event
	if err := json.Unmarshal(msg.Body, &evt); err != nil {
		slog.Warn("quarantine malformed notification event", "error", err)
		w.malformed.Add(1)
		return w.quarantine(msg, err)
	}
	if strings.TrimSpace(evt.EventID) == "" || strings.TrimSpace(evt.RecipientID) == "" || !validType(evt.Type) || evt.CreatedAt < 0 {
		w.malformed.Add(1)
		return w.quarantine(msg, model.ErrInvalidEvent)
	}
	ctx, cancel := context.WithTimeout(msg.Context(), storeTimeout)
	defer cancel()
	if err := w.svc.Store(ctx, evt); err != nil {
		if msg.DeliveryCount < maxDelivery && !errors.Is(err, model.ErrReplayConflict) && !errors.Is(err, model.ErrInvalidEvent) {
			slog.Warn("store notification failed, requeue", "delivery", msg.DeliveryCount, "error", err)
			w.requeue.Add(1)
			return msg.Nack(true)
		}
		return w.quarantine(msg, err)
	}
	w.ack.Add(1)
	return msg.Ack()
}

func validType(typ events.NotifType) bool {
	switch typ {
	case events.NotifLike, events.NotifComment, events.NotifFollow, events.NotifSystem:
		return true
	}
	return false
}

func (w *Worker) quarantine(msg *rabbitmq.Message, cause error) error {
	ctx, cancel := context.WithTimeout(msg.Context(), 3*time.Second)
	defer cancel()
	contentType := msg.ContentType
	if err := w.broker.PublishWithProperties(ctx, "/queues/"+dlqName, msg.Body, &amqp.MessageProperties{MessageID: msg.MessageID, ContentType: &contentType}); err != nil {
		slog.Error("dead-letter publish unconfirmed; retaining original", "error", err, "cause", cause)
		w.requeue.Add(1)
		return msg.Nack(true)
	}
	w.dlq.Add(1)
	return msg.Ack()
}

// StartMetrics 启动独立 /healthz + /metrics HTTP 端口（Prometheus 抓取）。
func (w *Worker) StartMetrics(addr string) *http.Server {
	if addr == "" {
		addr = "127.0.0.1:9092"
	}
	srv := &http.Server{Addr: addr, Handler: w.metricsHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("notification worker metrics listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("notification metrics server failed", "error", err)
		}
	}()
	return srv
}

func (w *Worker) metricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("ok"))
	})
	mux.HandleFunc("/metrics", func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/plain; version=0.0.4")
		line := func(name, help string, v int64) {
			fmt.Fprintf(rw, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
		}
		line("feedvideo_notif_received_total", "messages received from notifications queue", w.received.Load())
		line("feedvideo_notif_ack_total", "messages acked after store", w.ack.Load())
		line("feedvideo_notif_requeue_total", "messages requeued on store failure", w.requeue.Load())
		line("feedvideo_notif_dlq_total", "messages dead-lettered after max retries", w.dlq.Load())
		line("feedvideo_notif_malformed_total", "malformed messages dropped", w.malformed.Load())
		snapshot := w.consumerSnapshot()
		known := 0
		if snapshot.Known {
			known = 1
		}
		fmt.Fprintf(rw, "# HELP feedvideo_notif_consumers confirmed local active consumers; -1 when unknown\n# TYPE feedvideo_notif_consumers gauge\nfeedvideo_notif_consumers %d\n", snapshot.Active)
		fmt.Fprintf(rw, "# HELP feedvideo_notif_consumers_known whether local consumer state is confirmed\n# TYPE feedvideo_notif_consumers_known gauge\nfeedvideo_notif_consumers_known %d\n", known)
	})
	return mux
}
