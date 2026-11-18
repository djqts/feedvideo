package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetSummary returns the current actor's statistics and their matching estimate.
func (h *Handler) GetSummary(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	summary, err := h.svc.GetSummary(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "creator statistics unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": summary})
}
