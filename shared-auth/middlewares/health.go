package middlewares

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"gorm.io/gorm"
)

// Pinger is an optional dependency that can be health-checked.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthChecker holds references to all dependencies for health probing.
// Dependencies that implement Pinger are probed dynamically.
type HealthChecker struct {
	MySQLDB     *gorm.DB
	MongoDB     *mongo.Database
	RedisClient *redis.Client
	// Extra optional probes keyed by name.
	Extra map[string]Pinger
}

// componentStatus 单组件探测结果（不含任何连接串/用户名/地址，P1.1）。
type componentStatus struct {
	Status    string  `json:"status"`               // ok / unhealthy / not_configured
	LatencyMS float64 `json:"latency_ms,omitempty"` // 仅成功探测时输出
}

// probe 探测单个组件并返回状态与耗时（毫秒）。
func (h *HealthChecker) probe(ctx context.Context, name string, ping func(context.Context) error) componentStatus {
	start := time.Now()
	if err := ping(ctx); err != nil {
		return componentStatus{Status: "unhealthy"}
	}
	return componentStatus{
		Status:    "ok",
		LatencyMS: float64(time.Since(start).Microseconds()) / 1000.0,
	}
}

// probeAll 让各组件共享检查期限并独立执行，避免慢依赖耗尽其他探针的时间。
func (h *HealthChecker) probeAll(ctx context.Context) (map[string]componentStatus, bool) {
	components := map[string]componentStatus{}
	allOK := true
	type probeResult struct {
		name   string
		status componentStatus
	}
	results := make(chan probeResult, 3+len(h.Extra))
	pending := 0
	startProbe := func(name string, ping func(context.Context) error) {
		pending++
		go func() {
			results <- probeResult{name: name, status: h.probe(ctx, name, ping)}
		}()
	}

	if h.MySQLDB != nil {
		sqlDB, err := h.MySQLDB.DB()
		if err != nil {
			components["mysql"] = componentStatus{Status: "unhealthy"}
			allOK = false
		} else {
			startProbe("mysql", func(c context.Context) error {
				return sqlDB.PingContext(c)
			})
		}
	} else {
		components["mysql"] = componentStatus{Status: "not_configured"}
	}

	if h.MongoDB != nil {
		startProbe("mongodb", func(c context.Context) error {
			return h.MongoDB.Client().Ping(c, nil)
		})
	} else {
		components["mongodb"] = componentStatus{Status: "not_configured"}
	}

	if h.RedisClient != nil {
		startProbe("redis", func(c context.Context) error {
			_, err := h.RedisClient.Ping(c).Result()
			return err
		})
	} else {
		components["redis"] = componentStatus{Status: "not_configured"}
	}

	for name, pinger := range h.Extra {
		startProbe(name, pinger.Ping)
	}
	// Only this goroutine writes components and allOK. Wait for every probe so
	// no health-check work remains after the request completes.
	for i := 0; i < pending; i++ {
		result := <-results
		components[result.name] = result.status
		if result.status.Status != "ok" {
			allOK = false
		}
	}
	return components, allOK
}

// LivezHandler 进程存活探针：不探测任何外部依赖（P1.1）。
func (h *HealthChecker) LivezHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// ReadyzHandler 就绪探针：所有必需依赖正常才返回 200（P1.1）。
func (h *HealthChecker) ReadyzHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		components, allOK := h.probeAll(ctx)
		status := http.StatusOK
		overall := "ok"
		if !allOK {
			status = http.StatusServiceUnavailable
			overall = "unavailable"
		}
		c.JSON(status, gin.H{
			"status":     overall,
			"components": components,
		})
	}
}

// HealthHandler 详细健康检查：供人工与诊断使用，返回组件级状态与延迟（P1.1）。
// 响应只包含组件名/状态/延迟，绝不输出连接串、用户名、密码或内部地址。
func (h *HealthChecker) HealthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		components, allOK := h.probeAll(ctx)
		overall := "ok"
		if !allOK {
			overall = "degraded"
		}

		httpStatus := http.StatusOK
		if overall != "ok" {
			httpStatus = http.StatusServiceUnavailable
		}

		c.JSON(httpStatus, gin.H{
			"status":     overall,
			"components": components,
			"checked_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
}
