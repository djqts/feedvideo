package config

import (
	"fmt"
	"strings"
)

const (
	LikeStateModeLegacy = "legacy"
	LikeStateModeMongo  = "mongo"
)

// ParseLikeStateMode defaults to the expand release's legacy writer.
func ParseLikeStateMode(raw string) (string, error) {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return LikeStateModeLegacy, nil
	}
	if mode != LikeStateModeLegacy && mode != LikeStateModeMongo {
		return "", fmt.Errorf("LIKE_STATE_MODE must be legacy or mongo")
	}
	return mode, nil
}
