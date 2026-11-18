package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

const mediaDLQKey = "media:dlq"

// MediaDLQStore 媒体 DLQ 重放所需的最小 Redis 能力。
type MediaDLQStore interface {
	RPop(ctx context.Context, key string) (string, error)
	LPushRaw(ctx context.Context, key, raw string) error
}

// NewMediaDLQRedis 用 *cache.RedisClient 构造 MediaDLQStore 适配器。
func NewMediaDLQRedis(rdb interface {
	RPop(ctx context.Context, key string) (string, error)
	LPush(ctx context.Context, key string, values ...string) error
}) MediaDLQStore {
	return mediaDLQRedis{rdb: rdb}
}

// mediaDLQRedis 适配 *cache.RedisClient 到 MediaDLQStore。
type mediaDLQRedis struct {
	rdb interface {
		RPop(ctx context.Context, key string) (string, error)
		LPush(ctx context.Context, key string, values ...string) error
	}
}

func (m mediaDLQRedis) RPop(ctx context.Context, key string) (string, error) {
	return m.rdb.RPop(ctx, key)
}

func (m mediaDLQRedis) LPushRaw(ctx context.Context, key, raw string) error {
	return m.rdb.LPush(ctx, key, raw)
}

// MediaDLQHandler 提供媒体任务 DLQ 的审计重放入口（attempt 重置后回到任务队列）。
type MediaDLQHandler struct {
	store       MediaDLQStore
	durableMode bool
}

func NewMediaDLQHandler(store MediaDLQStore) *MediaDLQHandler {
	return &MediaDLQHandler{store: store}
}

func (h *MediaDLQHandler) SetDurableMode(enabled bool) { h.durableMode = enabled }

// Replay godoc
// @Summary 重放媒体 DLQ 任务（管理员，attempt 重置）
// @Tags 管理
// @Produce json
// @Param body body object false "limit 重放数量"
// @Success 200 {object} map[string]interface{} "重放数量"
// @Router /api/v1/admin/media/dlq/replay [post]
func (h *MediaDLQHandler) Replay(c *gin.Context) {
	if h.durableMode {
		c.JSON(http.StatusConflict, gin.H{"code": 409, "message": "legacy media tasks require import and reconciliation before Mongo processing"})
		return
	}
	var req struct {
		Limit int `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	ctx := c.Request.Context()
	replayed := 0
	for replayed < limit {
		raw, err := h.store.RPop(ctx, mediaDLQKey)
		if err != nil {
			break // redis.Nil：DLQ 已空
		}
		// 重置 attempt 后回任务队列（幂等：video_id 唯一，重复处理由 mediaVisibleFilter 兜底）
		var task map[string]any
		if json.Unmarshal([]byte(raw), &task) == nil {
			task["attempt"] = 0
			if data, err := json.Marshal(task); err == nil {
				if lerr := h.store.LPushRaw(ctx, "media:tasks", string(data)); lerr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": lerr.Error()})
					return
				}
			}
		}
		replayed++
	}
	log.Printf("[audit] admin media dlq replay user=%s count=%d", currentAdminUID(c), replayed)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"replayed": replayed}})
}
