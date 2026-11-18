package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	config "feedvideo.local/shared-config"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"

	"github.com/gin-gonic/gin"
)

const (
	dlqQueue     = "notifications_dlq"
	notifQueue   = "notifications"
	maxReplayAll = 1000
)

// DLQHandler replays dead-lettered notification messages back to the main queue.
type DLQHandler struct {
	broker *rabbitmq.Broker
}

func NewDLQHandler(broker *rabbitmq.Broker) *DLQHandler {
	return &DLQHandler{broker: broker}
}

// Replay godoc
// @Summary 重放死信通知
// @Description 将 notifications_dlq 中的消息重放回 notifications 队列（仅管理员）
// @Tags 管理后台
// @Produce json
// @Param count query int false "重放条数（默认 100，最大 1000）" default(100)
// @Success 200 {object} map[string]interface{} "重放结果 {requeued}"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Failure 503 {object} map[string]string "MQ 不可用"
// @Router /api/v1/admin/notifications/dlq/replay [post]
func (h *DLQHandler) Replay(c *gin.Context) {
	uidStr, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	isAdmin := false
	for _, id := range config.AppConfig.AdminIDs {
		if id == uidStr.(string) {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return
	}
	if h == nil || h.broker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "mq unavailable"})
		return
	}

	count, _ := strconv.Atoi(c.DefaultQuery("count", "100"))
	if count <= 0 || count > maxReplayAll {
		count = 100
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	n, err := h.broker.RequeueMessages(ctx, dlqQueue, notifQueue, count)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"requeued": n}})
}
