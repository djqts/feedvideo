package middlewares

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type AccountStateChecker func(context.Context, string) (int, error)

const verifiedAccountStateKey = "feedvideo.verified-account-state"

type verifiedAccountState struct {
	actor  string
	status int
}

func accountStateUnavailable(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
		"code": "AUTH_STATE_UNAVAILABLE", "message": "AUTH_STATE_UNAVAILABLE",
		"retryable": true, "request_id": c.GetString("request_id"),
	})
}

// RequireCurrentAccount checks the primary status for every protected request.
// The one verified actor/status decision is shared with downstream content guards.
func RequireCurrentAccount(check AccountStateChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := c.GetString("userID")
		id, err := strconv.ParseInt(actor, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != actor {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		if check == nil {
			accountStateUnavailable(c)
			return
		}
		status, err := check(c.Request.Context(), actor)
		if err != nil || c.Request.Context().Err() != nil || (status != 0 && status != 1 && status != 2) {
			accountStateUnavailable(c)
			return
		}
		if status == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "account disabled"})
			return
		}
		c.Set(verifiedAccountStateKey, verifiedAccountState{actor: actor, status: status})
		c.Next()
	}
}
