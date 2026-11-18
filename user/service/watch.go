package service

import (
	"context"
	"encoding/json"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
)

type WatchStore interface {
	Insert(context.Context, *model.WatchRecord) (string, error)
}
type WatchMedia struct {
	DurationMS     int64
	ContentVersion int64
	Public         bool
}
type WatchSource func(context.Context, string) (WatchMedia, error)
type WatchService struct {
	store  WatchStore
	source WatchSource
	now    func() time.Time
}

func NewWatchService(store WatchStore, source WatchSource) *WatchService {
	return &WatchService{store: store, source: source, now: time.Now}
}

func (s *WatchService) Ingest(ctx context.Context, actor string, raw []json.RawMessage) []model.WatchResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := s.now().UTC()
	results := make([]model.WatchResult, 0, len(raw))
	type mediaResult struct {
		media  WatchMedia
		failed bool
	}
	cache := map[string]mediaResult{}
	for _, data := range raw {
		var identity struct {
			EventID string `json:"event_id"`
		}
		_ = json.Unmarshal(data, &identity)
		result := model.WatchResult{EventID: identity.EventID, Status: "rejected", Code: "INVALID_SCHEMA"}
		event, code := events.DecodeWatchEvent(data)
		if !events.WatchActorValid(actor) {
			code = "INVALID_ACTOR"
		}
		if code == "" {
			code = event.Validate(now, 0)
		}
		if code != "" {
			result.Code = code
			results = append(results, result)
			continue
		}
		result.EventID = event.EventID
		if ctx.Err() != nil || s.source == nil || s.store == nil {
			result.Status = "retryable"
			result.Code = "STORAGE_UNAVAILABLE"
			results = append(results, result)
			continue
		}
		source, ok := cache[event.Payload.VideoID]
		if !ok {
			media, err := s.source(ctx, event.Payload.VideoID)
			source = mediaResult{media: media, failed: err != nil}
			cache[event.Payload.VideoID] = source
		}
		switch {
		case ctx.Err() != nil || source.failed:
			result.Status = "retryable"
			result.Code = "MEDIA_UNAVAILABLE"
		case !source.media.Public:
			result.Code = "VIDEO_UNAVAILABLE"
		case source.media.DurationMS <= 0 || source.media.DurationMS > 86400000 || source.media.ContentVersion <= 0:
			result.Status = "retryable"
			result.Code = "MEDIA_UNAVAILABLE"
		case source.media.ContentVersion != event.Payload.ContentVersion:
			result.Code = "CONTENT_VERSION_MISMATCH"
		default:
			if code := event.Validate(now, source.media.DurationMS); code != "" {
				result.Code = code
				break
			}
			status, err := s.store.Insert(ctx, &model.WatchRecord{WatchEvent: event, ActorKind: "user", ActorID: actor, ReceivedAt: now, DurationMS: source.media.DurationMS, PayloadHash: events.WatchPayloadHash(event)})
			switch {
			case err != nil:
				result.Status = "retryable"
				result.Code = "STORAGE_UNAVAILABLE"
			case status == "accepted" || status == "duplicate":
				result.Status = status
				result.Code = ""
			case status == "CONFLICT":
				result.Code = "CONFLICT"
			default:
				result.Status = "retryable"
				result.Code = "STORAGE_UNAVAILABLE"
			}
		}
		results = append(results, result)
	}
	return results
}
