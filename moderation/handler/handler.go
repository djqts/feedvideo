package handler

import (
	"net/http"
	"strconv"

	"feedvideo.local/moderation/service"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc      *service.ModerationService
	adminIDs []string
}

func NewHandler(svc *service.ModerationService, adminIDs []string) *Handler {
	return &Handler{svc: svc, adminIDs: adminIDs}
}

func (h *Handler) isAdmin(uid string) bool {
	for _, id := range h.adminIDs {
		if id == uid { return true }
	}
	return false
}

func currentUID(c *gin.Context) (string, bool) {
	v, ok := c.Get("userID")
	if !ok { return "", false }
	s, ok := v.(string)
	return s, ok && s != ""
}

func (h *Handler) denyNonAdmin(c *gin.Context) (string, bool) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return "", false
	}
	if !h.isAdmin(uid) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return "", false
	}
	return uid, true
}

// GetReports godoc
// @Summary 举报列表（管理员）
// @Tags 审核
// @Produce json
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "举报列表"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Router /api/v1/admin/reports [get]
func (h *Handler) GetReports(c *gin.Context) {
	if _, ok := h.denyNonAdmin(c); !ok { return }
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	reports, total, err := h.svc.ListReports(c.Request.Context(), page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"list": reports, "total": total, "page": page}})
}

// BanUser godoc
// @Summary 封禁/解封用户（管理员）
// @Tags 审核
// @Accept json
// @Produce json
// @Param uid path int true "用户 ID"
// @Param body body object true "banned: bool"
// @Success 200 {object} map[string]string "操作成功"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Router /api/v1/admin/users/{uid}/ban [put]
func (h *Handler) BanUser(c *gin.Context) {
	if _, ok := h.denyNonAdmin(c); !ok { return }
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil { c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid uid"}); return }
	var body struct{ Banned bool `json:"banned"` }
	if err := c.ShouldBindJSON(&body); err != nil { c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"}); return }
	if err := h.svc.BanUser(c.Request.Context(), uid, body.Banned); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()}); return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// MuteUser godoc
// @Summary 禁言/取消禁言用户（管理员）
// @Tags 审核
// @Accept json
// @Produce json
// @Param uid path int true "用户 ID"
// @Param body body object true "muted: bool"
// @Success 200 {object} map[string]string "操作成功"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Router /api/v1/admin/users/{uid}/mute [put]
func (h *Handler) MuteUser(c *gin.Context) {
	if _, ok := h.denyNonAdmin(c); !ok { return }
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil { c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid uid"}); return }
	var body struct{ Muted bool `json:"muted"` }
	if err := c.ShouldBindJSON(&body); err != nil { c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"}); return }
	if err := h.svc.MuteUser(c.Request.Context(), uid, body.Muted); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()}); return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}
