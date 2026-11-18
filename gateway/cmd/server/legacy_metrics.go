package main

import (
	"context"
	"feedvideo.local/gateway/internal/legacymetrics"
	"feedvideo.local/shared-auth/middlewares"
	config "feedvideo.local/shared-config"
	"feedvideo.local/shared-db/cache"
)

func installLegacyMetrics(metrics *middlewares.Metrics, redis *cache.RedisClient, mode string) {
	metrics.SetLegacyMetricsReader(func(ctx context.Context) middlewares.LegacyMetricsSnapshot {
		if redis == nil || redis.Client == nil {
			return middlewares.LegacyMetricsSnapshot{}
		}
		snapshot, err := legacymetrics.ReadGateway(ctx, redis, mode == config.MediaTaskModeLegacy)
		if err != nil {
			return middlewares.LegacyMetricsSnapshot{}
		}
		value := func(v legacymetrics.Value) middlewares.LegacyMetricValue {
			return middlewares.LegacyMetricValue{Value: v.Value, Known: v.Known}
		}
		return middlewares.LegacyMetricsSnapshot{ReadSuccess: true, InvalidValues: snapshot.InvalidValues, Succeeded: value(snapshot.Succeeded), Failed: value(snapshot.Failed), Retried: value(snapshot.Retried), DLQ: value(snapshot.DeadLettered), Reconcile: value(snapshot.Reconcile)}
	})
}
