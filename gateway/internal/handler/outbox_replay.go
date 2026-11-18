package handler

import (
	"context"
	"log"
	"net/http"

	"feedvideo.local/shared-db/outbox"

	"github.com/gin-gonic/gin"
)

// OutboxReplayStore 管理端重放所需的最小 outbox 能力。
type OutboxReplayStore interface {
	ListFailed(ctx context.Context, limit int) ([]outbox.Record, error)
	ReplayFailed(ctx context.Context, ids []int64) (int64, error)
}

// OutboxReplayHandler 提供 failed Outbox 事件的审计重放入口。
type OutboxReplayHandler struct {
	store OutboxReplayStore
}

func NewOutboxReplayHandler(store OutboxReplayStore) *OutboxReplayHandler {
	return &OutboxReplayHandler{store: store}
}

// Replay godoc
// @Summary 重放 failed 的 Outbox 事件（管理员）
// @Tags 管理
// @Produce json
// @Param body body object false "event_ids 指定重放；否则按 limit 取最早 failed"
// @Success 200 {object} map[string]interface{} "重放数量"
// @Failure 400 {object} map[string]string "参数错误"
// @Router /api/v1/admin/outbox/replay [post]
func (h *OutboxReplayHandler) Replay(c *gin.Context) {
	var req struct {
		EventIDs []int64 `json:"event_ids"`
		Limit    int     `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	ctx := c.Request.Context()
	if len(req.EventIDs) > 0 {
		count, err := h.store.ReplayFailed(ctx, req.EventIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
			return
		}
		log.Printf("[audit] admin outbox replay user=%s count=%d ids=%v", currentAdminUID(c), count, req.EventIDs)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"replayed": count}})
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := h.store.ListFailed(ctx, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	count, err := h.store.ReplayFailed(ctx, ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	log.Printf("[audit] admin outbox replay user=%s count=%d", currentAdminUID(c), count)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"replayed": count}})
}

// currentAdminUID 读取认证上下文中的用户 ID（RequireAdmin 前置保证为管理员）。
func currentAdminUID(c *gin.Context) string {
	v, _ := c.Get("userID")
	s, _ := v.(string)
	return s
}
