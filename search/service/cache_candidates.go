package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"

	feedmodel "feedvideo.local/search/model"
	feedrepo "feedvideo.local/search/repo"
	videorepo "feedvideo.local/video/repo"
)

const maxCandidateCacheBytes = 512 * 1024

// Cache only a bounded provider window and successful user category. Tags are
// always queried again; video bodies never confer current visibility.
type candidateEnvelope struct {
	FormatVersion int                            `json:"format_version"`
	Q             string                         `json:"q"`
	Type          string                         `json:"type"`
	Page          int64                          `json:"page"`
	Size          int64                          `json:"size"`
	Index         string                         `json:"index"`
	VideoOffset   int64                          `json:"video_offset"`
	TagOffset     int64                          `json:"tag_offset"`
	Video         *videorepo.SearchCandidatePage `json:"video,omitempty"`
	User          *feedmodel.UserCategory        `json:"user,omitempty"`
}

func (s *SearchService) candidateIndex() string {
	if s.cacheNamespace != "" {
		return s.cacheNamespace
	}
	return "videos"
}

func (s *SearchService) candidateCacheKey(q, typ string, page, size int64, cursor string, state cursorState) string {
	key := feedrepo.CacheKey(q, typ, page, size)
	if cursor != "" {
		key = fmt.Sprintf("continuation:v1:%d:%d:%s", state.VideoOffset, state.TagOffset, key)
	}
	if s.cacheNamespace != "" {
		key = "projection:" + s.cacheNamespace + ":" + key
	}
	return key
}

func videoWindow(typ string, size int64) int64 {
	switch typ {
	case "video":
		return 4 * size
	case "all":
		return 3 * size
	}
	return 0
}
func boundedLimit(offset, limit int64) int64 {
	if limit > 5000-offset {
		return 5000 - offset
	}
	return limit
}
func validCandidatePage(p *videorepo.SearchCandidatePage, offset, limit int64) bool {
	if p == nil || p.Offset != offset || p.Candidates == nil || int64(len(p.Candidates)) > limit || p.Total < 0 || (p.TotalRelation != "eq" && p.TotalRelation != "gte") {
		return false
	}
	for _, candidate := range p.Candidates {
		if candidate.SourceVersion < 0 || (candidate.Score != nil && (math.IsNaN(*candidate.Score) || math.IsInf(*candidate.Score, 0))) {
			return false
		}
	}
	return true
}

func (s *SearchService) readCandidateCache(raw []byte, q, typ string, page, size int64, state cursorState, cursor string) *candidateEnvelope {
	if len(raw) == 0 || len(raw) > maxCandidateCacheBytes {
		return nil
	}
	var env candidateEnvelope
	fields, ok := cacheObjectFields(raw)
	if !ok {
		return nil
	}
	if _, candidateFormat := fields["format_version"]; candidateFormat {
		for name := range fields {
			switch name {
			case "format_version", "q", "type", "page", "size", "index", "video_offset", "tag_offset", "video", "user":
			default:
				return nil
			}
		}
		if !cacheRequiredFields(fields, "format_version", "q", "type", "page", "size", "index", "video_offset", "tag_offset") {
			return nil
		}
		if videoRaw, exists := fields["video"]; exists {
			videoFields, ok := cacheObjectFields(videoRaw)
			if !ok || len(videoFields) != 5 || !cacheRequiredFields(videoFields, "Candidates", "Total", "TotalRelation", "Offset", "Exhausted") {
				return nil
			}
			var candidates []json.RawMessage
			if json.Unmarshal(videoFields["Candidates"], &candidates) != nil {
				return nil
			}
			for _, candidate := range candidates {
				candidateFields, ok := cacheObjectFields(candidate)
				if !ok || len(candidateFields) != 3 || !cacheRequiredFields(candidateFields, "ID", "SourceVersion") {
					return nil
				}
				if _, exists := candidateFields["Score"]; !exists {
					return nil
				}
			}
		}
		if userRaw, exists := fields["user"]; exists {
			userFields, ok := cacheObjectFields(userRaw)
			if !ok || len(userFields) != 4 || !cacheRequiredFields(userFields, "status", "items", "total", "has_more") {
				return nil
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&env) == nil {
			var trailing any
			if decoder.Decode(&trailing) != io.EOF || env.FormatVersion != 1 || env.Q != q || env.Type != typ || env.Page != page || env.Size != size || env.Index != s.candidateIndex() || env.VideoOffset != state.VideoOffset || env.TagOffset != state.TagOffset {
				return nil
			}
			if (env.Video != nil) != (typ == "video" || typ == "all") || (env.User != nil) != (typ == "user" || typ == "all") {
				return nil
			}
			if env.Video != nil && !validCandidatePage(env.Video, state.VideoOffset, boundedLimit(state.VideoOffset, videoWindow(typ, size))) {
				return nil
			}
			if env.User != nil && (env.User.Status != feedmodel.CategoryStatusOK || env.User.Items == nil || env.User.Total < 0 || int64(len(env.User.Items)) > size) {
				return nil
			}
			return &env
		}
		// A malformed candidate-format entry cannot masquerade as legacy v3.
		return nil
	}
	// Old v3 responses are accepted only at their C1 page identity. Extract
	// canonical video candidates from the requested video category, never from
	// unrelated legacy fields. Tag previews/counts are intentionally discarded.
	if cursor != "" {
		return nil
	}
	var old feedmodel.SearchResp
	if json.Unmarshal(raw, &old) != nil || !completeCategories(&old, typ, raw) {
		return nil
	}
	env = s.newCandidateEnvelope(q, typ, page, size, state)
	if cat := old.Categories.Video; cat != nil {
		if cat.Total < 0 || int64(len(cat.Items)) > boundedLimit(state.VideoOffset, videoWindow(typ, size)) {
			return nil
		}
		candidates := make([]videorepo.SearchCandidate, 0, len(cat.Items))
		for _, video := range cat.Items {
			if video == nil || video.ID.IsZero() {
				return nil
			}
			candidates = append(candidates, videorepo.SearchCandidate{ID: video.ID.Hex()})
		}
		env.Video = &videorepo.SearchCandidatePage{Candidates: candidates, Offset: state.VideoOffset, Total: cat.Total, TotalRelation: "eq", Exhausted: state.VideoOffset+int64(len(candidates)) >= cat.Total}
	}
	if cat := old.Categories.User; cat != nil {
		if cat.Total < 0 || int64(len(cat.Items)) > size {
			return nil
		}
		env.User = cat
	}
	return &env
}

// The normal typed decoder erases missing/null scalars and accepts duplicate
// keys. Preserve the actual bounded object's keys before typed conversion.
func cacheObjectFields(raw []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := token.(string)
		if !ok {
			return nil, false
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields[key] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}

func cacheRequiredFields(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

func (s *SearchService) newCandidateEnvelope(q, typ string, page, size int64, state cursorState) candidateEnvelope {
	return candidateEnvelope{FormatVersion: 1, Q: q, Type: typ, Page: page, Size: size, Index: s.candidateIndex(), VideoOffset: state.VideoOffset, TagOffset: state.TagOffset}
}
