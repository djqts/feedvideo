package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"

	feedmodel "feedvideo.local/search/model"
	feedrepo "feedvideo.local/search/repo"
)

type cursorState struct {
	VideoOffset int64 `json:"video_offset"`
	TagOffset   int64 `json:"tag_offset"`
	Page        int64 `json:"page"`
}

type cursorPayload struct {
	FormatVersion int64  `json:"format_version"`
	QHash         string `json:"q_hash"`
	Type          string `json:"type"`
	Size          int64  `json:"size"`
	Index         string `json:"index"`
	VideoOffset   int64  `json:"video_offset"`
	TagOffset     int64  `json:"tag_offset"`
	Page          int64  `json:"page"`
}

// page is the next request's page; offsets are candidate positions actually
// consumed by this request, including discarded duplicate and invalid IDs.
func encodeCandidateCursor(q, typ string, page, size, videoOffset, tagOffset int64, index string) string {
	if feedmodel.ValidateSearchRequest(q, typ, page, size) != nil || videoOffset < 0 || videoOffset > 4999 || tagOffset < 0 || tagOffset > 4999 {
		return ""
	}
	if index == "" {
		index = "videos"
	}
	hash := sha256.Sum256([]byte(feedrepo.Normalize(q)))
	payload := cursorPayload{FormatVersion: 1, QHash: hex.EncodeToString(hash[:]), Type: typ, Size: size, Index: index, VideoOffset: videoOffset, TagOffset: tagOffset, Page: page}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 768 {
		return ""
	}
	cursor := base64.RawURLEncoding.EncodeToString(raw)
	if len(cursor) > 1024 {
		return ""
	}
	return cursor
}

func invalidCandidateCursor() error {
	return &feedmodel.QueryValidationError{Parameter: "candidate_cursor", Reason: "must be a valid continuation matching this search"}
}

// ValidateCandidateCursorSyntax checks the wire and query identity before the
// handler selects an implementation. The service alone knows the target index.
func ValidateCandidateCursorSyntax(q, typ string, page, size int64, cursor string) error {
	_, err := readCandidateCursor(q, typ, page, size, cursor)
	return err
}

func ValidateCandidateCursor(q, typ string, page, size int64, cursor, index string) error {
	_, err := decodeCandidateCursor(q, typ, page, size, cursor, index)
	return err
}

func decodeCandidateCursor(q, typ string, page, size int64, cursor, index string) (cursorState, error) {
	if err := feedmodel.ValidateSearchRequest(q, typ, page, size); err != nil {
		return cursorState{}, err
	}
	if cursor == "" {
		return cursorState{VideoOffset: (page - 1) * size, TagOffset: (page - 1) * size, Page: page}, nil
	}
	payload, err := readCandidateCursor(q, typ, page, size, cursor)
	if err != nil {
		return cursorState{}, err
	}
	if index == "" {
		index = "videos"
	}
	if payload.Index != index {
		return cursorState{}, invalidCandidateCursor()
	}
	return cursorState{VideoOffset: payload.VideoOffset, TagOffset: payload.TagOffset, Page: payload.Page}, nil
}

func readCandidateCursor(q, typ string, page, size int64, cursor string) (cursorPayload, error) {
	var payload cursorPayload
	if err := feedmodel.ValidateSearchRequest(q, typ, page, size); err != nil {
		return payload, err
	}
	if len(cursor) == 0 || len(cursor) > 1024 {
		return payload, invalidCandidateCursor()
	}
	for _, ch := range cursor {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return payload, invalidCandidateCursor()
		}
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil || len(raw) > 768 {
		return payload, invalidCandidateCursor()
	}
	// Decode one bounded object with unique, required fields. Decoding integer
	// fields directly rejects fractions/exponents and null cannot stand for zero.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return payload, invalidCandidateCursor()
	}
	fields := make(map[string]json.RawMessage, 8)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return payload, invalidCandidateCursor()
		}
		key, ok := keyToken.(string)
		if !ok {
			return payload, invalidCandidateCursor()
		}
		if _, exists := fields[key]; exists {
			return payload, invalidCandidateCursor()
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return payload, invalidCandidateCursor()
		}
		fields[key] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return payload, invalidCandidateCursor()
	}
	if _, err = decoder.Token(); err != io.EOF {
		return payload, invalidCandidateCursor()
	}
	if len(fields) != 8 {
		return payload, invalidCandidateCursor()
	}
	for key, destination := range map[string]interface{}{
		"format_version": &payload.FormatVersion, "q_hash": &payload.QHash, "type": &payload.Type, "size": &payload.Size,
		"index": &payload.Index, "video_offset": &payload.VideoOffset, "tag_offset": &payload.TagOffset, "page": &payload.Page,
	} {
		value, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, destination) != nil {
			return payload, invalidCandidateCursor()
		}
	}
	hash := sha256.Sum256([]byte(feedrepo.Normalize(q)))
	if payload.FormatVersion != 1 || payload.QHash != hex.EncodeToString(hash[:]) || payload.Type != typ || payload.Size != size || payload.Page != page || payload.Index == "" || payload.VideoOffset < 0 || payload.VideoOffset > 4999 || payload.TagOffset < 0 || payload.TagOffset > 4999 || payload.Page < 1 || payload.Page > 100 {
		return payload, invalidCandidateCursor()
	}
	return payload, nil
}
