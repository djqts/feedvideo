package middlewares

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireSameOrigin 校验写请求的 Origin 与请求 Host 是否同源，防 Login CSRF。
// - 无 Origin 头（非浏览器客户端，如 curl / API 调用）放行；
// - Origin 与 Host 一致放行；
// - 命中 CORS 白名单（开发环境前端 dev server）放行；
// - 其余跨源写请求 403 + [origin-audit] 审计日志。
func RequireSameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		if originHost := hostOf(origin); originHost != "" && strings.EqualFold(originHost, c.Request.Host) {
			c.Next()
			return
		}
		if allowedOrigin(origin) {
			c.Next()
			return
		}
		log.Printf("[origin-audit] rejected origin=%q host=%q path=%s method=%s ip=%s",
			origin, c.Request.Host, c.Request.URL.Path, c.Request.Method, c.ClientIP())
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden origin"})
	}
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func allowedOrigin(origin string) bool {
	cfg := DefaultCORSConfig()
	for _, o := range cfg.AllowOrigins {
		if o == origin || o == "*" {
			return true
		}
	}
	return false
}
