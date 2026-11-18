package service

import (
	"context"
	"errors"
	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
	"feedvideo.local/user/repo"
)

type WatchQueryStore interface {
	History(context.Context, string, int, string) (*repo.WatchHistoryPage, error)
	Session(context.Context, string, string, string, int64) ([]*model.WatchRecord, error)
}
type WatchQueryService struct{ store WatchQueryStore }

func NewWatchQueryService(store WatchQueryStore) *WatchQueryService {
	return &WatchQueryService{store: store}
}

type WatchSessionResult struct {
	WatchedMS        int64    `json:"watched_ms"`
	CoveredMS        int64    `json:"covered_ms"`
	DurationMS       int64    `json:"duration_ms"`
	EffectivePlay    bool     `json:"effective_play"`
	CoverageComplete bool     `json:"coverage_complete"`
	CompletionRate   *float64 `json:"completion_rate"`
}

func (s *WatchQueryService) History(ctx context.Context, actor string, limit int, cursor string) (*repo.WatchHistoryPage, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("watch store unavailable")
	}
	return s.store.History(ctx, actor, limit, cursor)
}
func (s *WatchQueryService) Session(ctx context.Context, actor, session, video string, version int64) (*WatchSessionResult, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("watch store unavailable")
	}
	rows, err := s.store.Session(ctx, actor, session, video, version)
	if err != nil {
		return nil, err
	}
	facts := make([]events.WatchFact, 0, len(rows))
	for _, r := range rows {
		if r == nil || r.ActorID != actor || r.Payload.PlaybackSessionID != session || r.Payload.VideoID != video || r.Payload.ContentVersion != version {
			return nil, errors.New("mismatched watch fact")
		}
		facts = append(facts, events.WatchFact{ActorID: r.ActorID, Event: r.WatchEvent, DurationMS: r.DurationMS})
	}
	summary, err := events.AggregateWatch(facts)
	if err != nil {
		return nil, err
	}
	if len(summary) > 1 {
		return nil, errors.New("mixed watch session")
	}
	result := &WatchSessionResult{}
	if len(summary) == 1 {
		r := summary[0]
		result.WatchedMS = r.WatchedMS
		result.CoveredMS = r.CoveredMS
		result.DurationMS = r.DurationMS
		result.EffectivePlay = r.EffectivePlay
		result.CoverageComplete = r.CoverageComplete
	}
	result.CompletionRate = events.CompletionRate(summary)
	return result, nil
}

// WatchHistoryDTO exposes the wire facts, not internal payload hash/storage fields.
func WatchHistoryDTO(page *repo.WatchHistoryPage) map[string]any {
	list := make([]map[string]any, 0, len(page.Records))
	for _, r := range page.Records {
		event := map[string]any{"schema_version": r.SchemaVersion, "event_id": r.EventID, "type": r.Type, "occurred_at": r.OccurredAt, "payload": r.Payload, "received_at": r.ReceivedAt}
		list = append(list, event)
	}
	return map[string]any{"list": list, "has_more": page.HasMore, "next_cursor": page.NextCursor}
}
