package handler

import (
	"errors"
	"net/http"

	"feedvideo.local/video/service"
	"github.com/gin-gonic/gin"
)

// RetryMedia queues the author's failed Mongo job after validating its source.
func (h *VideoHandler) RetryMedia(c *gin.Context) {
	err := h.videoSvc.RetryMedia(c.Request.Context(), c.Param("id"), getOptionalUserID(c))
	if err != nil {
		status, message := http.StatusServiceUnavailable, "media temporarily unavailable"
		switch {
		case errors.Is(err, service.ErrMediaUnauthenticated):
			status, message = http.StatusUnauthorized, "authentication required"
		case errors.Is(err, service.ErrBadRequest):
			status, message = http.StatusBadRequest, "invalid video id"
		case errors.Is(err, service.ErrForbidden):
			status, message = http.StatusForbidden, "only the author can retry this media job"
		case errors.Is(err, service.ErrMediaNotFound):
			status, message = http.StatusNotFound, "video not found"
		case errors.Is(err, service.ErrMediaConflict):
			status, message = http.StatusConflict, "media job changed or is not failed"
		}
		c.JSON(status, gin.H{"code": status, "message": message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"media_status": "queued"}})
}
