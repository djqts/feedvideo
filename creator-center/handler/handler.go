package handler

import (
	"errors"
	"net/http"
	"strconv"

	"feedvideo.local/creator-center/service"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *service.CreatorService
}

func NewHandler(svc *service.CreatorService) *Handler { return &Handler{svc: svc} }

func currentUID(c *gin.Context) (string, bool) {
	v, ok := c.Get("userID")
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	uid, err := strconv.ParseInt(s, 10, 64)
	return s, err == nil && uid > 0 && strconv.FormatInt(uid, 10) == s
}

// GetStats godoc
// @Summary 创作者数据统计
// @Tags 创作者中心
// @Produce json
// @Success 200 {object} map[string]interface{} "播放/点赞/评论/收藏/粉丝统计"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/creator/stats [get]
func (h *Handler) GetStats(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	stats, err := h.svc.GetStats(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "creator statistics unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": stats})
}

// GetRevenue godoc
// @Summary 创作者收益估算
// @Tags 创作者中心
// @Produce json
// @Success 200 {object} map[string]interface{} "估算收益(CPM×播放/1000)"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/creator/revenue [get]
func (h *Handler) GetRevenue(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	rev, err := h.svc.GetRevenue(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "creator statistics unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": rev})
}

// VerifyUser godoc
// @Summary 创作者认证管理（管理员）
// @Tags 创作者中心
// @Accept json
// @Produce json
// @Param uid path int true "目标用户 ID"
// @Param body body object true "verified: bool"
// @Success 200 {object} map[string]string "操作成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Failure 404 {object} map[string]string "目标用户不存在"
// @Failure 413 {object} map[string]string "请求体超限"
// @Failure 503 {object} map[string]string "认证资料或依赖暂不可用"
// @Router /api/v1/creator/verify/{uid} [put]
func (h *Handler) VerifyUser(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	if !h.svc.IsAdmin(uid) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return
	}
	targetUID, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || targetUID <= 0 || strconv.FormatInt(targetUID, 10) != c.Param("uid") {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid uid"})
		return
	}
	verified, err := parseVerificationRequest(c.Writer, c.Request)
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "request body too large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	if err := h.svc.SetVerified(c.Request.Context(), targetUID, verified); err != nil {
		switch {
		case errors.Is(err, service.ErrVerificationInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid uid"})
		case errors.Is(err, service.ErrVerificationTargetNotFound):
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "target user not found"})
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "creator verification unavailable"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}
