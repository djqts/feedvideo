package middlewares

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// routeStat 单路由模板的请求统计（路由模板化，避免原始 ID 高基数）。
type routeStat struct {
	latency latencyHistogram
	err4    atomic.Int64
	err5    atomic.Int64
}

type routeMetricsSnapshot struct {
	name       string
	latency    latencySnapshot
	err4, err5 int64
}

// SearchProjectionSnapshot carries one complete generation of projection metrics.
// It is independent of the gateway scheduler and contains no live dependencies.
type SearchProjectionSnapshot struct {
	Pending, OldestSeconds                int64
	PendingKnown                          bool
	Succeeded, Failed, Conflicts, Expired int64
}

// SearchBackgroundSnapshot is one complete value with fixed, unlabelled fields.
type SearchBackgroundSnapshot struct {
	Running                                                                                   bool
	Queued, Active                                                                            int64
	Accepted, Completed, Failed, Dropped, Deduplicated, MissingIdentity, DedupCapacityDropped uint64
}

// Metrics collects Prometheus-style metrics.
// 禁止把 user_id、视频 ID、评论 ID、token 或原始 URL 作为 label（P1.2）。
type Metrics struct {
	startTime time.Time
	now       func() time.Time

	// 全局请求计数（保留向后兼容输出）
	requests   atomic.Int64
	errors4xx  atomic.Int64
	errors5xx  atomic.Int64
	latencySum atomic.Int64 // 微秒

	// 按路由模板统计
	mu     sync.RWMutex
	routes map[string]*routeStat

	// MQ
	mqSendOK   atomic.Int64
	mqSendFail atomic.Int64
	mqReceived atomic.Int64
	mqConsumer atomic.Int64
	mqPublish  latencyHistogram
	mqConsume  latencyHistogram

	// 上传
	uploadOK     atomic.Int64
	uploadFail   atomic.Int64
	uploadOrphan atomic.Int64

	// 降级 fallback（redis/es/mq...）
	fallbacks map[string]*atomic.Int64

	// Deprecated number-only reader, retained for source compatibility and never
	// called during a scrape because it cannot prove known state or a read budget.
	statReader          func(key string) int64
	legacyMetricsReader func(context.Context) LegacyMetricsSnapshot
	// searchProjectionReader is called once per scrape, never once per metric key.
	searchProjectionReader func() SearchProjectionSnapshot
	// Installed during startup and called once per scrape.
	searchBackgroundReader func() SearchBackgroundSnapshot
	// dbPoolReader 返回数据库连接池使用率（0-1），由 main 注入。
	dbPoolReader func() float64
}

// NewMetrics creates a new Metrics collector.
func NewMetrics() *Metrics {
	return newMetricsWithClock(time.Now)
}

func newMetricsWithClock(now func() time.Time) *Metrics {
	if now == nil {
		now = time.Now
	}
	return &Metrics{
		startTime: now(),
		now:       now,
		routes:    make(map[string]*routeStat),
		fallbacks: make(map[string]*atomic.Int64),
	}
}

// SetStatReader is deprecated; install SetLegacyMetricsReader for observations.
func (m *Metrics) SetStatReader(r func(key string) int64) {
	m.statReader = r
}

// SetSearchProjectionReader installs the startup reader for complete search snapshots.
func (m *Metrics) SetSearchProjectionReader(r func() SearchProjectionSnapshot) {
	m.searchProjectionReader = r
}

func (m *Metrics) SetSearchBackgroundReader(r func() SearchBackgroundSnapshot) {
	m.searchBackgroundReader = r
}

// SetDBPoolReader 注入数据库连接池使用率读取器。
func (m *Metrics) SetDBPoolReader(r func() float64) {
	m.dbPoolReader = r
}

func (m *Metrics) route(name string) *routeStat {
	m.mu.RLock()
	s, ok := m.routes[name]
	m.mu.RUnlock()
	if ok {
		return s
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok = m.routes[name]; ok {
		return s
	}
	s = &routeStat{}
	m.routes[name] = s
	return s
}

// Middleware returns a Gin middleware that records per-request metrics.
// 路由模板使用 c.FullPath()（如 /api/v1/videos/:id），拒绝原始 URL 标签。
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := m.now()
		c.Next()
		elapsed := m.now().Sub(start)

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		rs := m.route(route)
		rs.latency.observeDuration(elapsed)

		code := c.Writer.Status()
		if code >= 500 {
			m.errors5xx.Add(1)
			rs.err5.Add(1)
		} else if code >= 400 {
			m.errors4xx.Add(1)
			rs.err4.Add(1)
		}

		m.requests.Add(1)
		m.latencySum.Add(elapsed.Microseconds())
	}
}

// IncMQSendSuccess records a successfully published RabbitMQ message.
func (m *Metrics) IncMQSendSuccess() { m.mqSendOK.Add(1) }

// IncMQSendFailure records a failed RabbitMQ publish.
func (m *Metrics) IncMQSendFailure() { m.mqSendFail.Add(1) }

// IncMQReceived records a message consumed from RabbitMQ.
func (m *Metrics) IncMQReceived() { m.mqReceived.Add(1) }

// SetMQConsumers records the number of active RabbitMQ consumers.
func (m *Metrics) SetMQConsumers(count int) { m.mqConsumer.Store(int64(count)) }

// ObserveMQPublishLatency 记录一次 MQ 发布的耗时（秒）。
func (m *Metrics) ObserveMQPublishLatency(seconds float64) {
	m.mqPublish.observe(seconds)
}

// ObserveMQConsumeLatency 记录一次 MQ 消费处理的耗时（秒）。
func (m *Metrics) ObserveMQConsumeLatency(seconds float64) {
	m.mqConsume.observe(seconds)
}

// IncUploadSuccess / IncUploadFailure / IncUploadOrphan 上传生命周期计数。
func (m *Metrics) IncUploadSuccess() { m.uploadOK.Add(1) }
func (m *Metrics) IncUploadFailure() { m.uploadFail.Add(1) }
func (m *Metrics) IncUploadOrphan()  { m.uploadOrphan.Add(1) }

// IncFallback 记录一次依赖降级（target: redis/es/mq...）。
func (m *Metrics) IncFallback(target string) {
	m.mu.Lock()
	c, ok := m.fallbacks[target]
	if !ok {
		c = &atomic.Int64{}
		m.fallbacks[target] = c
	}
	m.mu.Unlock()
	c.Add(1)
}

// fallbackSnapshot 返回 fallback 计数快照（按名称排序，保证输出稳定）。
func (m *Metrics) fallbackSnapshot() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]int64, len(m.fallbacks))
	for k, v := range m.fallbacks {
		out[k] = v.Load()
	}
	return out
}

// routeSnapshot 返回按路由模板排序的统计快照。
func (m *Metrics) routeSnapshot() []routeMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]routeMetricsSnapshot, 0, len(m.routes))
	for k, v := range m.routes {
		out = append(out, routeMetricsSnapshot{k, v.latency.snapshot(), v.err4.Load(), v.err5.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func keysParts(keys []string, labels map[string]string) []string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return parts
}

// Handler returns a Gin handler that exposes metrics in Prometheus text format.
func (m *Metrics) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqs := m.requests.Load()
		err4 := m.errors4xx.Load()
		err5 := m.errors5xx.Load()
		latSum := m.latencySum.Load()
		uptime := m.now().Sub(m.startTime).Seconds()
		mqOK := m.mqSendOK.Load()
		mqFail := m.mqSendFail.Load()
		mqRecv := m.mqReceived.Load()
		mqCons := m.mqConsumer.Load()

		avgLat := float64(0)
		if reqs > 0 {
			avgLat = float64(latSum) / float64(reqs) / 1000.0 // 微秒 → 毫秒
		}

		var b strings.Builder
		fmt.Fprintf(&b, "# HELP feedvideo_http_requests_total Total HTTP requests.\n# TYPE feedvideo_http_requests_total counter\nfeedvideo_http_requests_total %d\n", reqs)
		fmt.Fprintf(&b, "# HELP feedvideo_http_errors_total HTTP errors by class.\n# TYPE feedvideo_http_errors_total counter\nfeedvideo_http_errors_total{class=\"4xx\"} %d\nfeedvideo_http_errors_total{class=\"5xx\"} %d\n", err4, err5)
		fmt.Fprintf(&b, "# HELP feedvideo_http_request_duration_ms_avg Average request duration in ms (since start).\n# TYPE feedvideo_http_request_duration_ms_avg gauge\nfeedvideo_http_request_duration_ms_avg %.2f\n", avgLat)
		fmt.Fprintf(&b, "# HELP feedvideo_uptime_seconds Process uptime in seconds.\n# TYPE feedvideo_uptime_seconds gauge\nfeedvideo_uptime_seconds %.0f\n", uptime)

		// 按路由模板的直方图 + 分位数（P1.2）
		routes := m.routeSnapshot()
		b.WriteString("# HELP feedvideo_http_request_duration_seconds Request latency histogram by route template.\n# TYPE feedvideo_http_request_duration_seconds histogram\n")
		for _, rs := range routes {
			if rs.latency.count == 0 {
				continue
			}
			labels := map[string]string{"route": rs.name}
			b.WriteString(fmtHistogram("feedvideo_http_request_duration_seconds", labels, rs.latency))
		}
		for _, quantile := range []struct {
			name string
			p    float64
		}{{"p50", 0.50}, {"p95", 0.95}, {"p99", 0.99}} {
			fmt.Fprintf(&b, "# HELP feedvideo_http_request_duration_%s Request latency %s by route template.\n# TYPE feedvideo_http_request_duration_%s gauge\n", quantile.name, quantile.name, quantile.name)
			for _, rs := range routes {
				if rs.latency.count > 0 {
					fmt.Fprintf(&b, "feedvideo_http_request_duration_%s{route=%q} %.4f\n", quantile.name, rs.name, percentile(rs.latency, quantile.p))
				}
			}
		}
		b.WriteString("# TYPE feedvideo_http_errors_total_route counter\n")
		for _, rs := range routes {
			if rs.latency.count > 0 {
				fmt.Fprintf(&b, "feedvideo_http_errors_total_route{route=%q,class=\"4xx\"} %d\nfeedvideo_http_errors_total_route{route=%q,class=\"5xx\"} %d\n", rs.name, rs.err4, rs.name, rs.err5)
			}
		}

		// MQ 基础计数（向后兼容）
		fmt.Fprintf(&b, "# HELP feedvideo_mq_send_success_total RabbitMQ messages published successfully.\n# TYPE feedvideo_mq_send_success_total counter\nfeedvideo_mq_send_success_total %d\n", mqOK)
		fmt.Fprintf(&b, "# HELP feedvideo_mq_send_failure_total RabbitMQ publish failures.\n# TYPE feedvideo_mq_send_failure_total counter\nfeedvideo_mq_send_failure_total %d\n", mqFail)
		fmt.Fprintf(&b, "# HELP feedvideo_mq_received_total RabbitMQ messages consumed.\n# TYPE feedvideo_mq_received_total counter\nfeedvideo_mq_received_total %d\n", mqRecv)
		fmt.Fprintf(&b, "# HELP feedvideo_mq_consumers Current number of active RabbitMQ consumers.\n# TYPE feedvideo_mq_consumers gauge\nfeedvideo_mq_consumers %d\n", mqCons)

		// MQ 延迟直方图（P1.2）
		if published := m.mqPublish.snapshot(); published.count > 0 {
			b.WriteString("# HELP feedvideo_mq_publish_duration_seconds MQ publish latency histogram.\n# TYPE feedvideo_mq_publish_duration_seconds histogram\n")
			b.WriteString(fmtHistogram("feedvideo_mq_publish_duration_seconds", map[string]string{"op": "publish"}, published))
		}
		if consumed := m.mqConsume.snapshot(); consumed.count > 0 {
			b.WriteString("# HELP feedvideo_mq_consume_duration_seconds MQ consume handling latency histogram.\n# TYPE feedvideo_mq_consume_duration_seconds histogram\n")
			b.WriteString(fmtHistogram("feedvideo_mq_consume_duration_seconds", map[string]string{"op": "consume"}, consumed))
		}

		// 上传生命周期（P1.2/P3.3）
		fmt.Fprintf(&b, "# HELP feedvideo_upload_success_total Successful media uploads.\n# TYPE feedvideo_upload_success_total counter\nfeedvideo_upload_success_total %d\n", m.uploadOK.Load())
		fmt.Fprintf(&b, "# HELP feedvideo_upload_failure_total Failed media uploads.\n# TYPE feedvideo_upload_failure_total counter\nfeedvideo_upload_failure_total %d\n", m.uploadFail.Load())
		fmt.Fprintf(&b, "# HELP feedvideo_upload_orphans_total Reaped orphan objects.\n# TYPE feedvideo_upload_orphans_total counter\nfeedvideo_upload_orphans_total %d\n", m.uploadOrphan.Load())

		// 一次抓取固定一个完整值快照，后台刷新不能混入这一组样本。
		projection := SearchProjectionSnapshot{Pending: -1, OldestSeconds: -1}
		if m.searchProjectionReader != nil {
			projection = m.searchProjectionReader()
		}
		pendingKnown := int64(0)
		if projection.PendingKnown {
			pendingKnown = 1
		} else {
			projection.Pending, projection.OldestSeconds = -1, -1
		}
		fmt.Fprintf(&b, "# HELP search_projection_pending Pending durable search intents, or -1 when unknown.\n# TYPE search_projection_pending gauge\nsearch_projection_pending %d\n", projection.Pending)
		fmt.Fprintf(&b, "# HELP search_projection_oldest_seconds Oldest pending search intent age, or -1 when unknown.\n# TYPE search_projection_oldest_seconds gauge\nsearch_projection_oldest_seconds %d\n", projection.OldestSeconds)
		fmt.Fprintf(&b, "# HELP search_projection_pending_known Whether the pending search intent snapshot is known.\n# TYPE search_projection_pending_known gauge\nsearch_projection_pending_known %d\n", pendingKnown)
		fmt.Fprintf(&b, "# HELP search_projection_success_total Successfully committed search projections.\n# TYPE search_projection_success_total counter\nsearch_projection_success_total %d\n", projection.Succeeded)
		fmt.Fprintf(&b, "# HELP search_projection_failure_total Failed search projection attempts.\n# TYPE search_projection_failure_total counter\nsearch_projection_failure_total %d\n", projection.Failed)
		fmt.Fprintf(&b, "# HELP search_projection_version_conflict_total Search projection version conflicts.\n# TYPE search_projection_version_conflict_total counter\nsearch_projection_version_conflict_total %d\n", projection.Conflicts)
		fmt.Fprintf(&b, "# HELP search_projection_expired_commit_total Rejected search projection lease commits.\n# TYPE search_projection_expired_commit_total counter\nsearch_projection_expired_commit_total %d\n", projection.Expired)

		background := SearchBackgroundSnapshot{}
		if m.searchBackgroundReader != nil {
			background = m.searchBackgroundReader()
		}
		running := 0
		if background.Running {
			running = 1
		}
		fmt.Fprintf(&b, "# HELP search_background_running Whether search background jobs are accepted.\n# TYPE search_background_running gauge\nsearch_background_running %d\n", running)
		fmt.Fprintf(&b, "# HELP search_background_queued Queued search background jobs.\n# TYPE search_background_queued gauge\nsearch_background_queued %d\n", background.Queued)
		fmt.Fprintf(&b, "# HELP search_background_active Active search background jobs.\n# TYPE search_background_active gauge\nsearch_background_active %d\n", background.Active)
		fmt.Fprintf(&b, "# HELP search_background_accepted_total Accepted search background jobs.\n# TYPE search_background_accepted_total counter\nsearch_background_accepted_total %d\n", background.Accepted)
		fmt.Fprintf(&b, "# HELP search_background_completed_total Finished search background jobs including failures.\n# TYPE search_background_completed_total counter\nsearch_background_completed_total %d\n", background.Completed)
		fmt.Fprintf(&b, "# HELP search_background_failed_total Failed search background jobs.\n# TYPE search_background_failed_total counter\nsearch_background_failed_total %d\n", background.Failed)
		fmt.Fprintf(&b, "# HELP search_background_dropped_total Rejected or discarded search background jobs.\n# TYPE search_background_dropped_total counter\nsearch_background_dropped_total %d\n", background.Dropped)
		fmt.Fprintf(&b, "# HELP search_background_deduplicated_total Suppressed duplicate search samples.\n# TYPE search_background_deduplicated_total counter\nsearch_background_deduplicated_total %d\n", background.Deduplicated)
		fmt.Fprintf(&b, "# HELP search_background_missing_identity_total Search samples suppressed without identity.\n# TYPE search_background_missing_identity_total counter\nsearch_background_missing_identity_total %d\n", background.MissingIdentity)
		fmt.Fprintf(&b, "# HELP search_background_dedup_capacity_dropped_total Search samples suppressed by a full deduplication table.\n# TYPE search_background_dedup_capacity_dropped_total counter\nsearch_background_dedup_capacity_dropped_total %d\n", background.DedupCapacityDropped)

		// 降级 fallback（P1.2）
		b.WriteString("# HELP feedvideo_fallback_total Dependency fallback events.\n# TYPE feedvideo_fallback_total counter\n")
		names := make([]string, 0)
		fb := m.fallbackSnapshot()
		for k := range fb {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			fmt.Fprintf(&b, "feedvideo_fallback_total{target=%q} %d\n", k, fb[k])
		}

		m.writeLegacyMetrics(&b, c.Request)

		if m.dbPoolReader != nil {
			fmt.Fprintf(&b, "# HELP feedvideo_db_pool_in_use_ratio Database pool utilization ratio (0-1).\n# TYPE feedvideo_db_pool_in_use_ratio gauge\nfeedvideo_db_pool_in_use_ratio %.4f\n", m.dbPoolReader())
		}

		c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(b.String()))
	}
}
