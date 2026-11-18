package middlewares

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireAdmin 创建一个 Gin 中间件，仅允许 adminIDs 白名单中的用户通过。
// 要求上游 AuthMiddleware 已设置 c.Set("userID", ...)，否则返回 403。
func RequireAdmin(adminIDs []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := c.Get("userID")
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
			return
		}
		uidStr, ok := uid.(string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
			return
		}
		for _, id := range adminIDs {
			if id == uidStr {
				log.Printf("[audit] admin action user=%s method=%s path=%s",
					uidStr, c.Request.Method, c.Request.URL.Path)
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
	}
}
