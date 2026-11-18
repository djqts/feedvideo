package middlewares

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ActiveChecker 判断用户是否处于可用状态（status=1）。
// 由网关注入 user 模块实现，避免 shared-auth 反向依赖 user。
type ActiveChecker func(ctx context.Context, uid string) bool

// RequireActiveAccount 认证后校验账号状态：被封禁/禁言用户立即失效，不再等 token 过期。
// 校验器返回 false（含明确禁用）→ 403；校验器内部对 DB 异常采取 fail-open（放行），
// 避免把基础设施故障放大成全体下线。
func RequireActiveAccount(check ActiveChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if check == nil {
			c.Next()
			return
		}
		uidVal, ok := c.Get("userID")
		uid, _ := uidVal.(string)
		if !ok || uid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		if !check(c.Request.Context(), uid) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "account disabled"})
			return
		}
		c.Next()
	}
}

// RequireNotMuted 禁言拦截（仅挂写端点）：checker 返回 true 表示"未被禁言"放行。
// 与 RequireActiveAccount 组合使用：被封(0)全拦，被禁言(2)仅拦写操作。
func RequireNotMuted(check ActiveChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if raw, found := c.Get(verifiedAccountStateKey); found {
			decision, valid := raw.(verifiedAccountState)
			if !valid || decision.actor != c.GetString("userID") {
				accountStateUnavailable(c)
				return
			}
			switch decision.status {
			case 1:
				c.Next()
			case 2:
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "muted"})
			default:
				accountStateUnavailable(c)
			}
			return
		}
		if check == nil {
			c.Next()
			return
		}
		uidVal, ok := c.Get("userID")
		uid, _ := uidVal.(string)
		if !ok || uid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		if !check(c.Request.Context(), uid) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "muted"})
			return
		}
		c.Next()
	}
}
