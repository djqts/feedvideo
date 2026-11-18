package middlewares

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// CSRFCookieName 双提交 CSRF token 的 Cookie 名（非 HttpOnly，供前端 JS 读取）。
const CSRFCookieName = "csrf_token"

// GenerateCSRFToken 生成 32 字节随机 CSRF token。
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 密码学随机数失败属于不可恢复错误，用 panic 暴露（启动即发现）。
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// SetCSRFCookie 下发 CSRF cookie：SameSite=Lax，HTTPS 下带 Secure；非 HttpOnly（JS 可读）。
func SetCSRFCookie(c *gin.Context, token string) {
	secure := strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CSRFCookieName, token, 7*24*60*60, "/", "", secure, false)
}

// CSRFTokenHandler GET /csrf-token：生成并下发新 token（幂等刷新）。
func CSRFTokenHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := GenerateCSRFToken()
		SetCSRFCookie(c, token)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"csrf_token": token}})
	}
}

// CSRFValidate enforces double-submit CSRF for Cookie-authenticated routes:
//   - 安全方法（GET/HEAD/OPTIONS）直接放行；
//   - Writes require exactly one valid CSRF Cookie and matching header.
//   - Missing, duplicate, malformed or mismatched tokens fail before handlers.
//
// Registration/login use their own Origin policy and do not mount this handler.
func CSRFValidate() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			c.Next()
			return
		}
		cookieCount := 0
		for _, line := range c.Request.Header.Values("Cookie") {
			for _, part := range strings.Split(line, ";") {
				name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
				if strings.TrimSpace(name) == CSRFCookieName {
					cookieCount++
				}
			}
		}
		rawCookie, err := c.Request.Cookie(CSRFCookieName)
		if cookieCount != 1 || err != nil || len(c.Request.Header.Values("X-CSRF-Token")) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "CSRF token missing or invalid"})
			return
		}
		// Gin's Cookie helper discards decoding errors, turning malformed values
		// into an empty token. Read the raw cookie so those errors fail closed.
		cookie, decodeErr := url.QueryUnescape(rawCookie.Value)
		header := c.GetHeader("X-CSRF-Token")
		_, formatErr := hex.DecodeString(cookie)
		if decodeErr != nil || formatErr != nil || len(cookie) != 64 || len(header) != 64 || subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "CSRF token missing or invalid",
			})
			return
		}
		c.Next()
	}
}
