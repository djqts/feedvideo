package config

import (
	"fmt"
	"strings"
)

const (
	MediaTaskModeLegacy = "legacy"
	MediaTaskModeMongo  = "mongo"
)

// ParseMediaTaskMode keeps the expand release in legacy mode unless explicitly enabled.
func ParseMediaTaskMode(raw string) (string, error) {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return MediaTaskModeLegacy, nil
	}
	if mode != MediaTaskModeLegacy && mode != MediaTaskModeMongo {
		return "", fmt.Errorf("MEDIA_TASK_MODE must be legacy or mongo")
	}
	return mode, nil
}
