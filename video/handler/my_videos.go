package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ListMyVideos godoc
// @Summary 我自己的视频（含待审核）
// @Tags 视频
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "视频列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/my/videos [get]
func (h *VideoHandler) ListMyVideos(c *gin.Context) {
	uid := getOptionalUserID(c)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "authentication required"})
		return
	}
	page, ok := publicListQueryInt(c, "page", 1, 100)
	if !ok {
		return
	}
	pageSize, ok := publicListQueryInt(c, "page_size", 20, 50)
	if !ok {
		return
	}
	resp, err := h.videoSvc.ListMyVideos(c.Request.Context(), uid, page, pageSize, c.Query("before"))
	if publicListError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}
