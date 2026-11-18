package handler

import (
	"errors"
	"net/http"
	"strconv"

	notifsvc "feedvideo.local/notification/service"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc              *notifsvc.Service
	adminIDs         map[string]bool
	allUsersProvider func() ([]string, error)
}

func NewHandler(svc *notifsvc.Service, adminIDs []string) *Handler {
	set := make(map[string]bool, len(adminIDs))
	for _, id := range adminIDs {
		if id != "" {
			set[id] = true
		}
	}
	return &Handler{svc: svc, adminIDs: set}
}

// SetAllUsersProvider 注入“全量用户 ID”提供者（网关侧由 MySQL 用户表实现），
// 用于系统通知广播全部（all=true）。
func (h *Handler) SetAllUsersProvider(fn func() ([]string, error)) {
	h.allUsersProvider = fn
}

func currentUID(c *gin.Context) (string, bool) {
	v, ok := c.Get("userID")
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// GetNotifications godoc
// @Summary 获取通知列表
// @Tags 通知
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param type query string false "类型过滤 like/comment/follow/system"
// @Param read query string false "all/unread" default(all)
// @Success 200 {object} map[string]interface{} "通知列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/notifications [get]
func (h *Handler) GetNotifications(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "20"), 10, 64)
	typ := c.Query("type")
	unreadOnly := c.DefaultQuery("read", "all") == "unread"

	list, total, err := h.svc.List(c.Request.Context(), uid, page, pageSize, typ, unreadOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
		"list": list, "total": total, "page": page,
	}})
}

// GetUnreadCount godoc
// @Summary 获取未读通知数
// @Tags 通知
// @Produce json
// @Success 200 {object} map[string]interface{} "按类型未读数 {by_type, total}"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/notifications/unread-count [get]
func (h *Handler) GetUnreadCount(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	counts, err := h.svc.UnreadCount(c.Request.Context(), uid)
	if err != nil {
		if errors.Is(err, notifsvc.ErrUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "notification unavailable"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": counts})
}

// MarkRead godoc
// @Summary 标记通知已读
// @Tags 通知
// @Accept json
// @Produce json
// @Param body body object true "ids(通知ID数组) 或 all=true 全部已读"
// @Success 200 {object} map[string]string "操作成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/notifications/read [post]
func (h *Handler) MarkRead(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	var body struct {
		IDs  []string `json:"ids"`
		All  bool     `json:"all"`
		Type string   `json:"type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	if !body.All && len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "ids or all is required"})
		return
	}
	if err := h.svc.MarkRead(c.Request.Context(), uid, body.IDs, body.All, body.Type); err != nil {
		if errors.Is(err, notifsvc.ErrUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "notification unavailable"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// PostSystem godoc
// @Summary 发送系统通知（管理员）
// @Tags 通知
// @Accept json
// @Produce json
// @Param body body object true "recipient_ids + content"
// @Success 200 {object} map[string]string "发送成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Router /api/v1/notifications/system [post]
func (h *Handler) PostSystem(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	if !h.adminIDs[uid] {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return
	}
	var body struct {
		RecipientIDs []string `json:"recipient_ids"`
		Content      string   `json:"content" binding:"required"`
		All          bool     `json:"all"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	recipients := body.RecipientIDs
	if body.All {
		if h.allUsersProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "broadcast to all not configured"})
			return
		}
		ids, err := h.allUsersProvider()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "resolve recipients failed"})
			return
		}
		recipients = ids
	} else if len(recipients) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "recipient_ids required when all=false"})
		return
	}
	if err := h.svc.System(c.Request.Context(), recipients, body.Content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}
