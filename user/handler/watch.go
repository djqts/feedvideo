package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
	"github.com/gin-gonic/gin"
)

type WatchIngester interface {
	Ingest(context.Context, string, []json.RawMessage) []model.WatchResult
}
type WatchAccountChecker func(context.Context, string) (bool, error)
type WatchHandler struct {
	ingester WatchIngester
	account  WatchAccountChecker
	queries  WatchQuerier
}

func NewWatchHandler(ingester WatchIngester, account WatchAccountChecker) *WatchHandler {
	return &WatchHandler{ingester: ingester, account: account}
}

func (h *WatchHandler) Ingest(c *gin.Context) {
	reply := func(status int, code string) {
		c.JSON(status, gin.H{"code": code, "message": code, "request_id": c.GetString("request_id")})
	}
	actor := c.GetString("userID")
	if !events.WatchActorValid(actor) {
		reply(401, "UNAUTHORIZED")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if h == nil || h.account == nil {
		reply(503, "STORAGE_UNAVAILABLE")
		return
	}
	active, err := h.account(ctx, actor)
	if err != nil || ctx.Err() != nil {
		reply(503, "ACCOUNT_UNAVAILABLE")
		return
	}
	if !active {
		reply(403, "ACCOUNT_DISABLED")
		return
	}
	if encoding := strings.TrimSpace(c.GetHeader("Content-Encoding")); encoding != "" && encoding != "identity" {
		reply(http.StatusUnsupportedMediaType, "UNSUPPORTED_ENCODING")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 65537))
	if len(raw) > 65536 {
		reply(http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE")
		return
	}
	if err != nil || !events.WatchJSONUnambiguous(raw) {
		reply(400, "INVALID_SCHEMA")
		return
	}
	var batch struct {
		Events []json.RawMessage `json:"events"`
	}
	if json.Unmarshal(raw, &batch) != nil || len(batch.Events) == 0 || len(batch.Events) > 50 {
		reply(400, "INVALID_BATCH")
		return
	}
	if h.ingester == nil {
		reply(503, "STORAGE_UNAVAILABLE")
		return
	}
	results := h.ingester.Ingest(ctx, actor, batch.Events)
	c.JSON(200, gin.H{"code": 0, "message": "ok", "request_id": c.GetString("request_id"), "data": gin.H{"results": results}})
}
