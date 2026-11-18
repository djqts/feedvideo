package handler

import (
	"context"
	"errors"
	events "feedvideo.local/shared-events"
	"feedvideo.local/user/repo"
	"feedvideo.local/user/service"
	"github.com/gin-gonic/gin"
	"net/url"
	"strconv"
	"time"
)

type WatchQuerier interface {
	History(context.Context, string, int, string) (*repo.WatchHistoryPage, error)
	Session(context.Context, string, string, string, int64) (*service.WatchSessionResult, error)
}

func (h *WatchHandler) WithQueries(queries WatchQuerier) *WatchHandler { h.queries = queries; return h }
func watchReadReply(c *gin.Context, status int, code string) {
	c.JSON(status, gin.H{"code": code, "message": code, "request_id": c.GetString("request_id")})
}
func (h *WatchHandler) watchReadContext(c *gin.Context) (context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	actor := c.GetString("userID")
	if !events.WatchActorValid(actor) {
		watchReadReply(c, 401, "UNAUTHORIZED")
		cancel()
		return ctx, func() {}, false
	}
	if h == nil || h.account == nil || h.queries == nil {
		watchReadReply(c, 503, "STORAGE_UNAVAILABLE")
		cancel()
		return ctx, func() {}, false
	}
	active, err := h.account(ctx, actor)
	if err != nil || ctx.Err() != nil {
		watchReadReply(c, 503, "ACCOUNT_UNAVAILABLE")
		cancel()
		return ctx, func() {}, false
	}
	if !active {
		watchReadReply(c, 403, "ACCOUNT_DISABLED")
		cancel()
		return ctx, func() {}, false
	}
	return ctx, cancel, true
}
func (h *WatchHandler) History(c *gin.Context) {
	ctx, cancel, ok := h.watchReadContext(c)
	if !ok {
		return
	}
	defer cancel()
	query, err := watchSingleQuery(c)
	if err != nil {
		watchReadReply(c, 400, "INVALID_QUERY")
		return
	}
	limit := 50
	if values, present := query["limit"]; present {
		raw := values[0]
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 || strconv.Itoa(value) != raw {
			watchReadReply(c, 400, "INVALID_QUERY")
			return
		}
		limit = value
	}
	cursor := c.Query("cursor")
	if _, present := query["cursor"]; present && cursor == "" {
		watchReadReply(c, 400, "INVALID_QUERY")
		return
	}
	page, err := h.queries.History(ctx, c.GetString("userID"), limit, cursor)
	if err != nil {
		h.watchReadError(c, err)
		return
	}
	if page == nil || ctx.Err() != nil {
		watchReadReply(c, 503, "STORAGE_UNAVAILABLE")
		return
	}
	c.JSON(200, gin.H{"code": 0, "message": "ok", "request_id": c.GetString("request_id"), "data": service.WatchHistoryDTO(page)})
}
func (h *WatchHandler) Session(c *gin.Context) {
	ctx, cancel, ok := h.watchReadContext(c)
	if !ok {
		return
	}
	defer cancel()
	query, err := watchSingleQuery(c)
	if err != nil {
		watchReadReply(c, 400, "INVALID_QUERY")
		return
	}
	if len(query["video_id"]) != 1 || len(query["content_version"]) != 1 {
		watchReadReply(c, 400, "INVALID_QUERY")
		return
	}
	raw := query["content_version"][0]
	version, err := strconv.ParseInt(raw, 10, 64)
	session, video := c.Param("session_id"), query["video_id"][0]
	probe := events.WatchEvent{SchemaVersion: 1, EventID: session, Type: "behavior.pause", OccurredAt: time.Now().UTC(), Payload: events.WatchPayload{PlaybackSessionID: session, VideoID: video, ContentVersion: version, PlaybackRate: 1}}
	if err != nil || strconv.FormatInt(version, 10) != raw || probe.Validate(probe.OccurredAt, 0) != "" {
		watchReadReply(c, 400, "INVALID_QUERY")
		return
	}
	result, err := h.queries.Session(ctx, c.GetString("userID"), session, video, version)
	if err != nil {
		h.watchReadError(c, err)
		return
	}
	if result == nil || ctx.Err() != nil {
		watchReadReply(c, 503, "STORAGE_UNAVAILABLE")
		return
	}
	c.JSON(200, gin.H{"code": 0, "message": "ok", "request_id": c.GetString("request_id"), "data": result})
}
func watchSingleQuery(c *gin.Context) (map[string][]string, error) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	for _, values := range query {
		if len(values) != 1 {
			return nil, repo.ErrWatchQueryInvalid
		}
	}
	return query, nil
}
func (h *WatchHandler) watchReadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrWatchQueryInvalid):
		watchReadReply(c, 400, "INVALID_QUERY")
	case errors.Is(err, repo.ErrWatchSessionTooLarge):
		watchReadReply(c, 422, "SESSION_TOO_LARGE")
	default:
		watchReadReply(c, 503, "STORAGE_UNAVAILABLE")
	}
}
