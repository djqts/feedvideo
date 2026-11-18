package events

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type WatchPayload struct {
	VideoID           string  `json:"video_id" bson:"video_id"`
	ContentVersion    int64   `json:"content_version" bson:"content_version"`
	PlaybackSessionID string  `json:"playback_session_id" bson:"playback_session_id"`
	Seq               int64   `json:"seq" bson:"seq"`
	PositionMS        int64   `json:"position_ms" bson:"position_ms"`
	WatchedDeltaMS    int64   `json:"watched_delta_ms" bson:"watched_delta_ms"`
	IntervalStartMS   int64   `json:"interval_start_ms" bson:"interval_start_ms"`
	IntervalEndMS     int64   `json:"interval_end_ms" bson:"interval_end_ms"`
	PlaybackRate      float64 `json:"playback_rate" bson:"playback_rate"`
	Visible           bool    `json:"visible" bson:"visible"`
	Foreground        bool    `json:"foreground" bson:"foreground"`
}
type WatchEvent struct {
	SchemaVersion int          `json:"schema_version" bson:"schema_version"`
	EventID       string       `json:"event_id" bson:"event_id"`
	Type          string       `json:"type" bson:"type"`
	OccurredAt    time.Time    `json:"occurred_at" bson:"occurred_at"`
	Payload       WatchPayload `json:"payload" bson:"payload"`
}

var watchUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var watchObjectID = regexp.MustCompile(`^[0-9a-f]{24}$`)
var watchPayloadKeys = []string{"video_id", "content_version", "playback_session_id", "seq", "position_ms", "watched_delta_ms", "interval_start_ms", "interval_end_ms", "playback_rate", "visible", "foreground"}
var watchEnvelopeKeys = []string{"schema_version", "event_id", "type", "occurred_at", "payload"}

// DecodeWatchEvent rejects missing/null fields and repeated JSON keys.
// Unknown optional fields are ignored; identity is supplied by the server.
func DecodeWatchEvent(raw []byte) (WatchEvent, string) {
	var e WatchEvent
	if !WatchJSONUnambiguous(raw) {
		return e, "INVALID_SCHEMA"
	}
	var envelope, payload map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || !watchRequired(envelope, watchEnvelopeKeys) || json.Unmarshal(envelope["payload"], &payload) != nil || !watchRequired(payload, watchPayloadKeys) {
		return e, "INVALID_SCHEMA"
	}
	for _, key := range watchPayloadKeys {
		if _, ok := envelope[key]; ok {
			return e, "INVALID_SCHEMA"
		}
	}
	for _, key := range watchEnvelopeKeys {
		if _, ok := payload[key]; ok {
			return e, "INVALID_SCHEMA"
		}
	}
	if json.Unmarshal(raw, &e) != nil {
		return WatchEvent{}, "INVALID_SCHEMA"
	}
	return e, ""
}
func watchRequired(fields map[string]json.RawMessage, keys []string) bool {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

// WatchJSONUnambiguous prevents different parsers selecting different repeated keys.
func WatchJSONUnambiguous(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func() bool
	walk = func() bool {
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				s, ok := key.(string)
				if err != nil || !ok || keys[s] {
					return false
				}
				keys[s] = true
				if !walk() {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !walk() {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !walk() {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
func WatchActorValid(raw string) bool {
	id, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == raw
}

// Validate takes server media duration in ms. Zero skips the duration boundary
// only, allowing invalid envelopes to be rejected before source I/O.
func (e WatchEvent) Validate(now time.Time, durationMS int64) string {
	if e.SchemaVersion != 1 {
		return "UNSUPPORTED_VERSION"
	}
	p := e.Payload
	if !watchUUID.MatchString(e.EventID) || !watchUUID.MatchString(p.PlaybackSessionID) || !watchObjectID.MatchString(p.VideoID) || strings.Trim(p.VideoID, "0") == "" || p.ContentVersion <= 0 || p.Seq < 0 || e.OccurredAt.IsZero() {
		return "INVALID_SCHEMA"
	}
	switch e.Type {
	case "behavior.impression", "behavior.play_start", "behavior.watch_progress", "behavior.pause", "behavior.complete", "behavior.skip", "behavior.not_interested_video":
	default:
		return "INVALID_SCHEMA"
	}
	_, offset := e.OccurredAt.Zone()
	if offset != 0 {
		return "INVALID_SCHEMA"
	}
	if e.OccurredAt.Before(now.Add(-7 * 24 * time.Hour)) {
		return "TOO_OLD"
	}
	if e.OccurredAt.After(now.Add(5 * time.Minute)) {
		return "FUTURE_EVENT"
	}
	if p.PositionMS < 0 || p.IntervalStartMS < 0 || p.IntervalEndMS < p.IntervalStartMS || p.WatchedDeltaMS < 0 || p.WatchedDeltaMS > 30000 || math.IsNaN(p.PlaybackRate) || math.IsInf(p.PlaybackRate, 0) || p.PlaybackRate < 0.25 || p.PlaybackRate > 4 {
		return "INVALID_PROGRESS"
	}
	if durationMS > 0 && (p.PositionMS > durationMS+500 || p.IntervalEndMS > durationMS+500) {
		return "INVALID_PROGRESS"
	}
	if e.Type == "behavior.watch_progress" {
		if !p.Visible || !p.Foreground || p.WatchedDeltaMS == 0 || p.IntervalEndMS == p.IntervalStartMS || p.PositionMS < p.IntervalEndMS-500 || p.PositionMS > p.IntervalEndMS+500 || math.Abs(float64(p.IntervalEndMS-p.IntervalStartMS)-float64(p.WatchedDeltaMS)*p.PlaybackRate) > 500 {
			return "INVALID_PROGRESS"
		}
	} else if p.WatchedDeltaMS != 0 || p.IntervalStartMS != p.IntervalEndMS {
		return "INVALID_PROGRESS"
	}
	return ""
}

// Hash excludes transport identity and server receipt time.
func WatchPayloadHash(e WatchEvent) string {
	raw, _ := json.Marshal(struct {
		SchemaVersion int          `json:"schema_version"`
		Type          string       `json:"type"`
		OccurredAt    string       `json:"occurred_at"`
		Payload       WatchPayload `json:"payload"`
	}{e.SchemaVersion, e.Type, e.OccurredAt.UTC().Format(time.RFC3339Nano), e.Payload})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
