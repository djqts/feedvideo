package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	userrepo "feedvideo.local/user/repo"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"feedvideo.local/video/service"
)

// LikeHandler 处理点赞相关 HTTP 请求
type LikeHandler struct {
	likeSvc      *service.LikeService
	behaviorRepo *userrepo.UserBehavior
}

func likeActor(c *gin.Context, strict bool) (string, bool) {
	value, exists := c.Get("userID")
	actor, ok := value.(string)
	if !exists || !ok || actor == "" || (strict && !service.ValidLikeActor(actor)) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return "", false
	}
	return actor, true
}

func likeStateData(state *model.LikeState) gin.H {
	if state == nil {
		return gin.H{"active": false, "version": int64(0)}
	}
	return gin.H{"target_id": state.TargetID, "target_type": state.TargetType, "active": state.Active, "version": state.Version}
}

func writeLikeError(c *gin.Context, err error, durable bool) {
	var conflict *repo.LikeVersionConflict
	switch {
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{"code": 409, "message": "like version conflict", "data": gin.H{"current": likeStateData(conflict.Current)}})
	case errors.Is(err, repo.ErrLikeInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid like request"})
	case errors.Is(err, service.ErrLikeTargetNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "target not found"})
	case errors.Is(err, repo.ErrLikeBackpressure):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "like notification backlog full; retry later"})
	case errors.Is(err, repo.ErrLikeUnavailable) || durable:
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "like state temporarily unavailable"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "like state temporarily unavailable"})
	}
}

func parseLikeIfMatch(header http.Header) (*int64, error) {
	values := header.Values("If-Match")
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, repo.ErrLikeInvalid
	}
	raw := strings.TrimSpace(values[0])
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" {
		return nil, repo.ErrLikeInvalid
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return nil, repo.ErrLikeInvalid
		}
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, repo.ErrLikeInvalid
	}
	return &version, nil
}

// SetLikeState applies the explicit final state and optional version condition.
func (h *LikeHandler) SetLikeState(c *gin.Context) {
	actor, ok := likeActor(c, true)
	if !ok {
		return
	}
	var body struct {
		TargetID   string `json:"target_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
		Active     *bool  `json:"active" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Active == nil || !validTargetType(body.TargetType) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "target_id, target_type and active are required"})
		return
	}
	expected, err := parseLikeIfMatch(c.Request.Header)
	if err != nil {
		writeLikeError(c, err, true)
		return
	}
	if !h.likeSvc.DurableLikesEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "durable like state is not enabled"})
		return
	}
	state, err := h.likeSvc.SetLikeState(c.Request.Context(), &service.LikeReq{UserID: actor, TargetID: body.TargetID, TargetType: body.TargetType}, *body.Active, expected)
	if err != nil {
		writeLikeError(c, err, true)
		return
	}
	c.Header("ETag", strconv.Quote(strconv.FormatInt(state.Version, 10)))
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": likeStateData(state)})
	if *body.Active {
		if uid, err := strconv.ParseInt(actor, 10, 64); err == nil {
			userrepo.LogAsync(h.behaviorRepo, uid, "like", body.TargetID, body.TargetType)
		}
	}
}

// validTargetType 点赞目标类型白名单，防止任意字符串增殖 Redis key / 污染 LikeRecord。
func validTargetType(t string) bool {
	return t == "video" || t == "comment"
}

// NewLikeHandler 创建 LikeHandler
func NewLikeHandler(likeSvc *service.LikeService, behaviorRepo *userrepo.UserBehavior) *LikeHandler {
	return &LikeHandler{likeSvc: likeSvc, behaviorRepo: behaviorRepo}
}

// Like godoc
// @Summary 点赞（视频或评论）
// @Tags 互动
// @Accept json
// @Produce json
// @Param body body object true "target_id + target_type(video/comment)"
// @Success 200 {object} map[string]string "点赞成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/likes [post]
func (h *LikeHandler) Like(c *gin.Context) {
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	var body struct {
		TargetID   string `json:"target_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	if !validTargetType(body.TargetType) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid target_type, must be video or comment"})
		return
	}
	req := &service.LikeReq{
		UserID:     userID,
		TargetID:   body.TargetID,
		TargetType: body.TargetType,
	}
	if err := h.likeSvc.Like(c.Request.Context(), req); err != nil {
		writeLikeError(c, err, h.likeSvc.DurableLikesEnabled())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	// Log behavior (only on like, not unlike — this is the Like method)
	if uid, err := strconv.ParseInt(userID, 10, 64); err == nil {
		userrepo.LogAsync(h.behaviorRepo, uid, "like", body.TargetID, body.TargetType)
	}
}

// Unlike godoc
// @Summary 取消点赞
// @Tags 互动
// @Accept json
// @Produce json
// @Param body body object true "target_id + target_type(video/comment)"
// @Success 200 {object} map[string]string "取消成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/likes [delete]
func (h *LikeHandler) Unlike(c *gin.Context) {
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	var body struct {
		TargetID   string `json:"target_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	if !validTargetType(body.TargetType) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid target_type, must be video or comment"})
		return
	}
	req := &service.LikeReq{
		UserID:     userID,
		TargetID:   body.TargetID,
		TargetType: body.TargetType,
	}
	if err := h.likeSvc.Unlike(c.Request.Context(), req); err != nil {
		writeLikeError(c, err, h.likeSvc.DurableLikesEnabled())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// IsLiked godoc
// @Summary 查询是否已点赞
// @Tags 互动
// @Produce json
// @Param target_id query string true "目标 ID"
// @Param target_type query string true "video/comment"
// @Success 200 {object} map[string]interface{} "is_liked"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/likes/check [get]
func (h *LikeHandler) IsLiked(c *gin.Context) {
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	targetID := c.Query("target_id")
	targetType := c.Query("target_type")
	if targetID == "" || targetType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "target_id and target_type are required"})
		return
	}
	if !validTargetType(targetType) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid target_type, must be video or comment"})
		return
	}
	if h.likeSvc.DurableLikesEnabled() {
		state, err := h.likeSvc.GetLikeState(c.Request.Context(), userID, targetID, targetType)
		if err != nil {
			writeLikeError(c, err, true)
			return
		}
		data := likeStateData(state)
		data["is_liked"] = state.Active
		c.Header("ETag", strconv.Quote(strconv.FormatInt(state.Version, 10)))
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
		return
	}
	liked, err := h.likeSvc.IsLiked(c.Request.Context(), userID, targetID, targetType)
	if err != nil {
		writeLikeError(c, err, h.likeSvc.DurableLikesEnabled())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"is_liked": liked}})
}

// GetLikeCount godoc
// @Summary 获取点赞数量（公开）
// @Tags 互动
// @Produce json
// @Param target_id query string true "目标 ID"
// @Param target_type query string true "video/comment"
// @Success 200 {object} map[string]interface{} "count"
// @Failure 400 {object} map[string]string "参数错误"
// @Router /api/v1/likes/count [get]
func (h *LikeHandler) GetLikeCount(c *gin.Context) {
	targetID := c.Query("target_id")
	targetType := c.Query("target_type")
	if targetID == "" || targetType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "target_id and target_type are required"})
		return
	}
	if !validTargetType(targetType) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid target_type, must be video or comment"})
		return
	}
	count, err := h.likeSvc.GetLikeCount(c.Request.Context(), targetID, targetType)
	if err != nil {
		writeLikeError(c, err, h.likeSvc.DurableLikesEnabled())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"count": count}})
}
