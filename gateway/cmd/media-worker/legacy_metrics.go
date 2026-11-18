package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"feedvideo.local/gateway/internal/legacymetrics"
	"feedvideo.local/shared-db/cache"
	"github.com/redis/go-redis/v9"
)

func newLegacyMediaMetricsHandler(rdb *redis.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		unavailable := func() { http.Error(w, "media metrics unavailable", http.StatusServiceUnavailable) }
		if rdb == nil {
			unavailable()
			return
		}
		snapshot, err := legacymetrics.ReadMedia(req.Context(), &cache.RedisClient{Client: rdb})
		if err != nil || req.Context().Err() != nil {
			unavailable()
			return
		}
		var b, knownLines strings.Builder
		known := func(key string, value bool) {
			n := 0
			if value {
				n = 1
			}
			fmt.Fprintf(&knownLines, "feedvideo_legacy_metric_known{scope=\"media-worker\",key=%q} %d\n", key, n)
		}
		line := func(name, help, kind string, value int64) {
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s %d\n", name, help, name, kind, name, value)
		}
		line("feedvideo_media_active", "Unsupported legacy active observation; -1 is unknown.", "gauge", -1)
		known("media:active", false)
		for _, item := range []struct {
			key, name string
			value     legacymetrics.Value
		}{
			{"media:retried", "feedvideo_media_retried_total", snapshot.Retried},
			{"media:failed", "feedvideo_media_failed_total", snapshot.Failed},
			{"media:succeeded", "feedvideo_media_succeeded_total", snapshot.Succeeded},
			{"media:dlq", "feedvideo_media_dlq_total", snapshot.DeadLettered},
		} {
			fmt.Fprintf(&b, "# HELP %s Recorded legacy Redis events, without a durable completeness guarantee.\n# TYPE %s counter\n", item.name, item.name)
			if item.value.Known {
				fmt.Fprintf(&b, "%s %d\n", item.name, item.value.Value)
			}
			known(item.key, item.value.Known)
		}
		line("feedvideo_media_tasks_queue_depth", "Observed pending Redis list length.", "gauge", snapshot.Tasks)
		known("queue:tasks", true)
		line("feedvideo_media_processing_depth", "Observed processing Redis list length.", "gauge", snapshot.Processing)
		known("queue:processing", true)
		line("feedvideo_media_dlq_depth", "Observed dead-letter Redis list length.", "gauge", snapshot.DLQ)
		known("queue:dlq", true)
		age := int64(-1)
		ageKnown := snapshot.Tasks == 0 && snapshot.Processing == 0
		if ageKnown {
			age = 0
		}
		line("feedvideo_media_oldest_task_age_seconds", "Zero for empty observed queues; otherwise unknown (-1).", "gauge", age)
		known("queue:oldest_age", ageKnown)
		for _, status := range []string{"queued", "processing", "succeeded", "failed"} {
			line("feedvideo_video_status_"+status, "Unsupported legacy current video status count; -1 is unknown.", "gauge", -1)
			known("video:status:"+status, false)
		}
		b.WriteString("# HELP feedvideo_legacy_metrics_read_success Legacy fixed-key snapshot read succeeded.\n# TYPE feedvideo_legacy_metrics_read_success gauge\nfeedvideo_legacy_metrics_read_success{scope=\"media-worker\"} 1\n")
		b.WriteString("# HELP feedvideo_legacy_metrics_invalid_values Invalid optional legacy cache values.\n# TYPE feedvideo_legacy_metrics_invalid_values gauge\n")
		fmt.Fprintf(&b, "feedvideo_legacy_metrics_invalid_values{scope=\"media-worker\"} %d\n", snapshot.InvalidValues)
		b.WriteString("# HELP feedvideo_legacy_metric_known Legacy sample known within its documented observation scope.\n# TYPE feedvideo_legacy_metric_known gauge\n")
		b.WriteString(knownLines.String())
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(b.String()))
	})
	// Keep 250ms for a complete 503 before the server's one-second write limit.
	mux.Handle("/metrics", http.TimeoutHandler(handler, 750*time.Millisecond, "media metrics unavailable\n"))
	return mux
}
