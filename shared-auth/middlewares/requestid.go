package middlewares

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestIDKey gin context 中 request_id 的键名。
const RequestIDKey = "request_id"

// GenerateRequestID 生成 16 字节随机请求 ID（hex）。
func GenerateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// RequestIDMiddleware 读取/生成 X-Request-Id 并写入上下文与响应头，
// 供访问日志与后续链路追踪（OTel）复用同一关联键。
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" || len(id) > 128 {
			id = GenerateRequestID()
		}
		c.Set(RequestIDKey, id)
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

// RequestID 从 context 取 request_id（未设置时返回空）。
func RequestID(c *gin.Context) string {
	v, _ := c.Get(RequestIDKey)
	s, _ := v.(string)
	return s
}

// RequestIDFromRequest 便捷函数：请求级（非 gin）场景取 header。
func RequestIDFromRequest(r *http.Request) string {
	return r.Header.Get("X-Request-Id")
}
