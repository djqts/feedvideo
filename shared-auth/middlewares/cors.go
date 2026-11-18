package middlewares

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig holds CORS middleware configuration.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig returns a secure default CORS configuration.
// 生产（APP_ENV=production）allowlist 只保留 CORS_ALLOWED_ORIGINS 指定的真实
// HTTPS 前端域名；开发环境保留 localhost 来源（P2.2）。
func DefaultCORSConfig() CORSConfig {
	if env := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")); env != "" {
		var origins []string
		for _, o := range strings.Split(env, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
		return CORSConfig{
			AllowOrigins:     origins,
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Content-Type", "X-CSRF-Token", "Authorization", "If-Match", "Idempotency-Key", "X-FeedVideo-Actor"},
			AllowCredentials: true,
			MaxAge:           86400,
		}
	}
	if strings.EqualFold(os.Getenv("APP_ENV"), "production") {
		// 生产：同源部署（nginx 同域提供前端与 API），无跨域需求 → 空 allowlist。
		return CORSConfig{
			AllowOrigins:     nil,
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Content-Type", "X-CSRF-Token", "Authorization", "If-Match", "Idempotency-Key", "X-FeedVideo-Actor"},
			AllowCredentials: true,
			MaxAge:           86400,
		}
	}
	return CORSConfig{
		AllowOrigins:     []string{"http://localhost:80", "http://127.0.0.1:80", "http://localhost:5173", "http://127.0.0.1:5173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "X-CSRF-Token", "Authorization", "If-Match", "Idempotency-Key", "X-FeedVideo-Actor"},
		AllowCredentials: true,
		MaxAge:           86400,
	}
}

// CORS returns a Gin middleware that sets CORS headers.
func CORS(cfg CORSConfig) gin.HandlerFunc {
	allowOrigin := func(origin string) bool {
		for _, o := range cfg.AllowOrigins {
			if o == origin || o == "*" {
				return true
			}
		}
		return false
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" && allowOrigin(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
		} else if len(cfg.AllowOrigins) == 1 && cfg.AllowOrigins[0] == "*" {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			// 未允许的 Origin：不返回 Allow-Origin，并记录审计日志（P2.2）
			log.Printf("[cors-audit] disallowed origin=%q path=%s method=%s ip=%s",
				origin, c.Request.URL.Path, c.Request.Method, c.ClientIP())
		}
		// 缓存键包含 Origin，必须显式 Vary（避免 CDN/浏览器误复用）
		c.Header("Vary", "Origin")

		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, Authorization, If-Match, Idempotency-Key, X-FeedVideo-Actor")
		c.Header("Access-Control-Expose-Headers", "ETag")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
