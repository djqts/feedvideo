package config

import (
	"fmt"
	"strings"
)

const (
	SearchProjectionModeLegacy   = "legacy"
	SearchProjectionModeMongo    = "mongo"
	DefaultSearchProjectionIndex = "videos_projection_v1"
)

func ParseSearchProjectionMode(raw string) (string, error) {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return SearchProjectionModeLegacy, nil
	}
	if mode != SearchProjectionModeLegacy && mode != SearchProjectionModeMongo {
		return "", fmt.Errorf("SEARCH_PROJECTION_MODE must be legacy or mongo")
	}
	return mode, nil
}

// Projection writes use an isolated, concrete lower-case ES index, never the legacy videos target.
func ParseSearchProjectionIndex(raw string) (string, error) {
	index := strings.TrimSpace(raw)
	if index == "" {
		return DefaultSearchProjectionIndex, nil
	}
	if index == "videos" || len(index) > 255 || !((index[0] >= 'a' && index[0] <= 'z') || (index[0] >= '0' && index[0] <= '9')) {
		return "", fmt.Errorf("SEARCH_PROJECTION_INDEX must be an isolated Elasticsearch index")
	}
	for _, c := range index {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return "", fmt.Errorf("SEARCH_PROJECTION_INDEX contains an invalid character")
		}
	}
	return index, nil
}
