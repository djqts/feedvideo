package middlewares

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// matchExpectedActor binds a page's confirmed identity to the verified access
// subject. A header never authenticates a request or replaces the access JWT.
func matchExpectedActor(c *gin.Context, verifiedActor string) bool {
	path := c.FullPath()
	if (c.Request.Method == http.MethodGet && path == "/api/v1/auth/me") ||
		(c.Request.Method == http.MethodPost && path == "/logout") {
		return true
	}
	values := c.Request.Header.Values("X-FeedVideo-Actor")
	if len(values) == 0 {
		// Older clients retain their existing access-cookie authentication.
		return true
	}
	expected := ""
	valid := len(values) == 1
	if valid {
		expected = values[0]
		if expected != "anonymous" {
			id, err := strconv.ParseInt(expected, 10, 64)
			valid = err == nil && id > 0 && strconv.FormatInt(id, 10) == expected
		}
	}
	if !valid || (verifiedActor != "" && expected != verifiedActor) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"code": "AUTH_ACTOR_MISMATCH", "message": "AUTH_ACTOR_MISMATCH",
		})
		return false
	}
	if verifiedActor == "" && expected != "anonymous" {
		// An expected account cannot silently become an anonymous optional read.
		// Preserve the 401 renewal flow without treating refresh as authentication.
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid access token"})
		return false
	}
	return true
}
