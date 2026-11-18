package handler

import (
	"errors"
	"net/http"
	"strconv"

	userrepo "feedvideo.local/user/repo"
	"feedvideo.local/video/repo"

	"github.com/gin-gonic/gin"
)

type ShareHandler struct {
	videoRepo    repo.VideoRepo
	behaviorRepo *userrepo.UserBehavior
}

func NewShareHandler(videoRepo repo.VideoRepo, behaviorRepo *userrepo.UserBehavior) *ShareHandler {
	return &ShareHandler{videoRepo: videoRepo, behaviorRepo: behaviorRepo}
}

// Share godoc
// @Summary 分享视频
// @Tags 互动
// @Produce json
// @Param video_id path string true "视频 ID"
// @Success 200 {object} map[string]interface{} "分享成功 {share_count}"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/share/{video_id} [post]
func (h *ShareHandler) Share(c *gin.Context) {
	videoID := c.Param("video_id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video_id is required"})
		return
	}

	// 分享计数统一走 stats 管道（HIncrBy 原子自增，替代原 Get+Set 非原子读改写）
	if err := h.videoRepo.IncrementStat(c.Request.Context(), videoID, "share_count", 1); err != nil {
		if errors.Is(err, repo.ErrOrdinaryStatsUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "statistics unavailable"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "share failed"})
		return
	}
	n := int64(0)
	if st, err := h.videoRepo.GetStats(c.Request.Context(), videoID); err == nil {
		n = st.ShareCount
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"share_count": n}})
	// Log behavior
	uid, _ := c.Get("userID")
	if uid != nil {
		if uidStr, ok := uid.(string); ok {
			if uidInt, err := strconv.ParseInt(uidStr, 10, 64); err == nil && uidInt > 0 {
				userrepo.LogAsync(h.behaviorRepo, uidInt, "share", videoID, "video")
			}
		}
	}
}
