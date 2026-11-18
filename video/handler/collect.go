package handler

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	userrepo "feedvideo.local/user/repo"
	"feedvideo.local/video/repo"
	"feedvideo.local/video/service"
	"github.com/gin-gonic/gin"
)

type CollectHandler struct {
	svc          *service.CollectService
	behaviorRepo *userrepo.UserBehavior
}

func NewCollectHandler(svc *service.CollectService, behaviorRepo *userrepo.UserBehavior) *CollectHandler {
	return &CollectHandler{svc: svc, behaviorRepo: behaviorRepo}
}
func collectActor(c *gin.Context) (string, bool) {
	value, exists := c.Get("userID")
	actor, ok := value.(string)
	if !exists || !ok || !service.ValidLikeActor(actor) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return "", false
	}
	return actor, true
}
func writeCollectError(c *gin.Context, err error) {
	var conflict *repo.CollectVersionConflict
	switch {
	case errors.As(err, &conflict):
		current := conflict.Current
		collected, version := false, "0"
		if current != nil {
			collected = current.Collected
			version = strconv.FormatInt(current.Version, 10)
		}
		c.Header("ETag", strconv.Quote(version))
		c.JSON(http.StatusConflict, gin.H{"code": 409, "message": "collect version conflict", "data": gin.H{"collected": collected, "version": version}})
	case errors.Is(err, repo.ErrCollectInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid collect request"})
	case errors.Is(err, service.ErrCollectTargetNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "target not found"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "collect state temporarily unavailable"})
	}
}
func parseCollectIfMatch(header http.Header) (*int64, error) {
	values := header.Values("If-Match")
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, repo.ErrCollectInvalid
	}
	raw := strings.TrimSpace(values[0])
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 0 || strconv.FormatInt(version, 10) != raw {
		return nil, repo.ErrCollectInvalid
	}
	return &version, nil
}
func (h *CollectHandler) writeResult(c *gin.Context, actor, videoID string, result *service.CollectResult) {
	c.Header("ETag", strconv.Quote(result.Version))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": result})
	if result.Changed && result.Collected {
		uid, _ := strconv.ParseInt(actor, 10, 64)
		userrepo.LogAsync(h.behaviorRepo, uid, "collect", videoID, "video")
	}
}

// Collect retains the legacy POST toggle. Explicit PUT is safe to retry.
// Collect godoc
// @Summary 收藏视频
// @Tags 收藏
// @Accept json
// @Produce json
// @Param body body object true "video_id"
// @Success 200 {object} map[string]interface{} "收藏结果"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/collect [post]
func (h *CollectHandler) Collect(c *gin.Context) {
	actor, ok := collectActor(c)
	if !ok {
		return
	}
	var body struct {
		VideoID string `json:"video_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || !service.ValidCollectVideoID(body.VideoID) {
		writeCollectError(c, repo.ErrCollectInvalid)
		return
	}
	result, err := h.svc.Collect(c.Request.Context(), actor, body.VideoID)
	if err != nil {
		writeCollectError(c, err)
		return
	}
	h.writeResult(c, actor, body.VideoID, result)
}

// SetCollectState godoc
// @Summary 设置收藏状态（条件更新）
// @Tags 收藏
// @Accept json
// @Produce json
// @Param If-Match header string false "版本号"
// @Param body body object true "video_id + collected"
// @Success 200 {object} map[string]interface{} "收藏结果"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/collect [put]
func (h *CollectHandler) SetCollectState(c *gin.Context) {
	actor, ok := collectActor(c)
	if !ok {
		return
	}
	var body struct {
		VideoID   string `json:"video_id"`
		Collected *bool  `json:"collected"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Collected == nil || !service.ValidCollectVideoID(body.VideoID) {
		writeCollectError(c, repo.ErrCollectInvalid)
		return
	}
	expected, err := parseCollectIfMatch(c.Request.Header)
	if err != nil {
		writeCollectError(c, err)
		return
	}
	result, err := h.svc.SetCollectState(c.Request.Context(), actor, body.VideoID, *body.Collected, expected)
	if err != nil {
		writeCollectError(c, err)
		return
	}
	h.writeResult(c, actor, body.VideoID, result)
}

// Check godoc
// @Summary 查询是否已收藏
// @Tags 收藏
// @Produce json
// @Param video_id query string true "视频 ID"
// @Success 200 {object} map[string]interface{} "收藏状态"
// @Failure 400 {object} map[string]string "参数错误"
// @Router /api/v1/collect/check [get]
func (h *CollectHandler) Check(c *gin.Context) {
	actor, ok := collectActor(c)
	if !ok {
		return
	}
	videoID := c.Query("video_id")
	if !service.ValidCollectVideoID(videoID) {
		writeCollectError(c, repo.ErrCollectInvalid)
		return
	}
	result, err := h.svc.GetCollectState(c.Request.Context(), actor, videoID)
	if err != nil {
		writeCollectError(c, err)
		return
	}
	c.Header("ETag", strconv.Quote(result.Version))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": result})
}
func collectPageParam(c *gin.Context, key string, fallback int64) (int64, error) {
	values, exists := c.Request.URL.Query()[key]
	if !exists {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, repo.ErrCollectInvalid
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || value < 1 {
		return 0, repo.ErrCollectInvalid
	}
	return value, nil
}

// List godoc
// @Summary 收藏列表
// @Tags 收藏
// @Produce json
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(10)
// @Success 200 {object} map[string]interface{} "收藏列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/collect/list [get]
func (h *CollectHandler) List(c *gin.Context) {
	actor, ok := collectActor(c)
	if !ok {
		return
	}
	page, err := collectPageParam(c, "page", 1)
	if err != nil {
		writeCollectError(c, err)
		return
	}
	size, err := collectPageParam(c, "size", 10)
	if err != nil || size > 100 || page > math.MaxInt64/size {
		writeCollectError(c, repo.ErrCollectInvalid)
		return
	}
	result, err := h.svc.List(c.Request.Context(), actor, page, size)
	if err != nil {
		writeCollectError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": result})
}
