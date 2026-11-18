package handler

import (
	"context"
	"net/http"
	"strconv"

	videomodel "feedvideo.local/video/model"
	videoservice "feedvideo.local/video/service"

	"github.com/gin-gonic/gin"
)

type RecommendHandler struct {
	svc      recommendService
	videoSvc videoDetailsService
}

type recommendService interface {
	ForYou(ctx context.Context, userID string, size int64) ([]*videomodel.Video, error)
	Similar(ctx context.Context, videoID, userID string, size int64) ([]*videomodel.Video, error)
}

type videoDetailsService interface {
	ListDetails(ctx context.Context, videos []*videomodel.Video, userID string) []*videoservice.VideoDetail
}

func New(svc recommendService) *RecommendHandler {
	return &RecommendHandler{svc: svc}
}

func (h *RecommendHandler) SetVideoService(svc videoDetailsService) { h.videoSvc = svc }

func (h *RecommendHandler) listResponse(ctx context.Context, videos []*videomodel.Video, userID string) any {
	if h.videoSvc != nil {
		return h.videoSvc.ListDetails(ctx, videos, userID)
	}
	return videos
}

// ForYou godoc
// @Summary 个性化推荐（For You）
// @Description 协同过滤 + 内容 + 热度三路召回并加权排序
// @Tags 推荐
// @Produce json
// @Param size query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "推荐视频列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/recommend/for-you [get]
func (h *RecommendHandler) ForYou(c *gin.Context) {
	uid, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "not authenticated"})
		return
	}
	userID := uid.(string)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)

	videos, err := h.svc.ForYou(c.Request.Context(), userID, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"list": h.listResponse(c.Request.Context(), videos, userID)}})
}

// Similar godoc
// @Summary 相似视频推荐
// @Description 根据目标视频标签返回相似视频
// @Tags 推荐
// @Produce json
// @Param video_id path string true "视频 ID"
// @Param size query int false "数量" default(10)
// @Success 200 {object} map[string]interface{} "相似视频列表"
// @Router /api/v1/recommend/similar/{video_id} [get]
func (h *RecommendHandler) Similar(c *gin.Context) {
	videoID := c.Param("video_id")
	if videoID == "" || len(videoID) != 24 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid video_id"})
		return
	}
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "10"), 10, 64)

	userID := ""
	if uid, ok := c.Get("userID"); ok {
		userID = uid.(string)
	}

	videos, err := h.svc.Similar(c.Request.Context(), videoID, userID, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"list": h.listResponse(c.Request.Context(), videos, userID)}})
}
