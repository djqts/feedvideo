package middlewares

import (
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// AccessLogMiddleware logs each HTTP request with structured fields.
func AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		if isSearchPath(path) {
			query = ""
		}

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		method := c.Request.Method
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()
		userID, _ := c.Get("userID")
		requestID := RequestID(c)

		log.Printf("[ACCESS] method=%s path=%s query=%s status=%d latency=%s client_ip=%s user_agent=%s user_id=%v request_id=%s",
			method, path, query, status, latency, clientIP, userAgent, userID, requestID,
		)
	}
}

func isSearchPath(path string) bool {
	return path == "/api/v1/search" || strings.HasPrefix(path, "/api/v1/search/") ||
		path == "/api/v1/videos/search" || path == "/search/users"
}
