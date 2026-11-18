package middlewares

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Search panics retain correlation fields without panic values or request dumps.
func RecoveryMiddleware() gin.HandlerFunc {
	ordinary := gin.Recovery()
	search := func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				if c.Writer.Written() {
					c.Abort()
				} else {
					c.AbortWithStatus(http.StatusInternalServerError)
				}
				log.Printf("[RECOVERY] search request failed method=%s path=%s status=%d request_id=%s",
					c.Request.Method, c.Request.URL.Path, c.Writer.Status(), RequestID(c))
			}
		}()
		c.Next()
	}
	return func(c *gin.Context) {
		if isSearchPath(c.Request.URL.Path) {
			search(c)
			return
		}
		ordinary(c)
	}
}
