package middlewares

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type LegacyMetricValue struct {
	Value int64
	Known bool
}
type LegacyMetricsSnapshot struct {
	ReadSuccess                                bool
	InvalidValues                              int64
	Succeeded, Failed, Retried, DLQ, Reconcile LegacyMetricValue
}

// SetLegacyMetricsReader installs the startup-only context-aware value reader.
func (m *Metrics) SetLegacyMetricsReader(reader func(context.Context) LegacyMetricsSnapshot) {
	m.legacyMetricsReader = reader
}

func (m *Metrics) writeLegacyMetrics(b *strings.Builder, req *http.Request) {
	ctx := context.Background()
	if req != nil {
		ctx = req.Context()
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	snap := LegacyMetricsSnapshot{}
	if m.legacyMetricsReader != nil && ctx.Err() == nil {
		snap = m.legacyMetricsReader(ctx)
	}
	if ctx.Err() != nil || !snap.ReadSuccess || snap.InvalidValues < 0 {
		snap = LegacyMetricsSnapshot{InvalidValues: -1}
	}
	for _, field := range []*LegacyMetricValue{&snap.Succeeded, &snap.Failed, &snap.Retried, &snap.DLQ, &snap.Reconcile} {
		if field.Known && field.Value < 0 {
			field.Known = false
			if snap.ReadSuccess {
				snap.InvalidValues++
			}
		}
	}
	success := 0
	if snap.ReadSuccess {
		success = 1
	}
	b.WriteString("# HELP feedvideo_legacy_metrics_read_success Legacy fixed-key snapshot read succeeded.\n# TYPE feedvideo_legacy_metrics_read_success gauge\n")
	fmt.Fprintf(b, "feedvideo_legacy_metrics_read_success{scope=\"gateway\"} %d\n", success)
	b.WriteString("# HELP feedvideo_legacy_metrics_invalid_values Invalid optional legacy cache values; -1 when read is unknown.\n# TYPE feedvideo_legacy_metrics_invalid_values gauge\n")
	fmt.Fprintf(b, "feedvideo_legacy_metrics_invalid_values{scope=\"gateway\"} %d\n", snap.InvalidValues)
	var knownLines strings.Builder
	known := func(key string, value bool) {
		n := 0
		if value {
			n = 1
		}
		fmt.Fprintf(&knownLines, "feedvideo_legacy_metric_known{scope=\"gateway\",key=%q} %d\n", key, n)
	}
	for _, item := range []struct{ key, name string }{
		{"media:active", "feedvideo_media_worker_active"},
		{"outbox:pending", "feedvideo_outbox_pending"},
		{"outbox:oldest_age", "feedvideo_outbox_oldest_age_seconds"},
	} {
		fmt.Fprintf(b, "# HELP %s Unsupported legacy real-time observation; -1 is unknown.\n# TYPE %s gauge\n%s -1\n", item.name, item.name, item.name)
		known(item.key, false)
	}
	b.WriteString("# HELP feedvideo_video_processing Unsupported legacy current video status counts; -1 is unknown.\n# TYPE feedvideo_video_processing gauge\n")
	for _, status := range []string{"uploaded", "validated", "queued", "processing", "succeeded", "failed"} {
		fmt.Fprintf(b, "feedvideo_video_processing{status=%q} -1\n", status)
		known("video:status:"+status, false)
	}
	for _, item := range []struct {
		key, name string
		value     LegacyMetricValue
	}{
		{"media:succeeded", "feedvideo_media_worker_succeeded_total", snap.Succeeded},
		{"media:failed", "feedvideo_media_worker_failed_total", snap.Failed},
		{"media:retried", "feedvideo_media_worker_retried_total", snap.Retried},
		{"media:dlq", "feedvideo_media_worker_dlq_total", snap.DLQ},
	} {
		fmt.Fprintf(b, "# HELP %s Recorded legacy Redis events, without a durable completeness guarantee.\n# TYPE %s counter\n", item.name, item.name)
		if item.value.Known {
			fmt.Fprintf(b, "%s %d\n", item.name, item.value.Value)
		}
		known(item.key, item.value.Known)
	}
	reconcile := int64(-1)
	if snap.Reconcile.Known {
		reconcile = snap.Reconcile.Value
	}
	b.WriteString("# HELP feedvideo_reconcile_stats_diff Most recently published cached reconciliation sample; -1 is unknown.\n# TYPE feedvideo_reconcile_stats_diff gauge\n")
	fmt.Fprintf(b, "feedvideo_reconcile_stats_diff %d\n", reconcile)
	known("reconcile:stats_diff", snap.Reconcile.Known)
	b.WriteString("# HELP feedvideo_legacy_metric_known Legacy sample known within its documented observation scope.\n# TYPE feedvideo_legacy_metric_known gauge\n")
	b.WriteString(knownLines.String())
}
