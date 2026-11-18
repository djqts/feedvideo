package handler

import (
	"errors"
	"net/http"

	"feedvideo.local/video/repo"
	"feedvideo.local/video/service"

	"github.com/gin-gonic/gin"
)

// AdminListVideos godoc
// @Summary 待审核视频列表（管理员）
// @Description 返回指定审核状态的视频，默认 pending，用于管理后台审核
// @Tags 管理
// @Produce json
// @Param status query string false "pending/rejected/approved" default(pending)
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "视频列表"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 503 {object} map[string]string "读取不可用"
// @Router /api/v1/admin/videos [get]
func (h *VideoHandler) AdminListVideos(c *gin.Context) {
	page, ok := publicListQueryInt(c, "page", 1, 100)
	if !ok {
		return
	}
	pageSize, ok := publicListQueryInt(c, "page_size", 20, 100)
	if !ok {
		return
	}
	status := c.DefaultQuery("status", "pending")
	resp, err := h.videoSvc.ListVideosForAdmin(c.Request.Context(), status, page, pageSize)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrBadRequest):
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		case errors.Is(err, repo.ErrVideoReadUnavailable):
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "video list unavailable"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "video list failed"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}
