package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"feedvideo.local/shared-db/elastic"
)

const (
	sensitiveWordsIndex    = "sensitive_words"
	sensitiveWordsPageSize = 100
	// Respect Elasticsearch's default result window. Larger dictionaries report
	// an incomplete check instead of silently treating a truncated scan as clean.
	sensitiveWordsMaxWindow = 10000
)

// SensitiveChecker reports whether text contains any sensitive word.
type SensitiveChecker interface {
	ContainsSensitive(ctx context.Context, text string) (bool, error)
}

// ESRepo is the minimal Elasticsearch dependency of the checker.
// *elastic.ESRepo satisfies it.
type ESRepo interface {
	Search(ctx context.Context, index string, body io.Reader) (*elastic.SearchResponse, error)
}

// esSensitiveChecker reads complete words from _source. The analyzed text field
// cannot decide substring membership: "上传" and "传销" share a Chinese token.
type esSensitiveChecker struct {
	es ESRepo
}

func NewESSensitiveChecker(es ESRepo) SensitiveChecker {
	return &esSensitiveChecker{es: es}
}

func (c *esSensitiveChecker) ContainsSensitive(ctx context.Context, text string) (bool, error) {
	if c == nil || c.es == nil || text == "" {
		return false, nil
	}
	for offset := 0; offset < sensitiveWordsMaxWindow; {
		query := map[string]any{
			"query":            map[string]any{"match_all": map[string]any{}},
			"_source":          []string{"word"},
			"sort":             []string{"_doc"},
			"from":             offset,
			"size":             min(sensitiveWordsPageSize, sensitiveWordsMaxWindow-offset),
			"track_total_hits": true,
		}
		body, err := json.Marshal(query)
		if err != nil {
			return false, err
		}
		result, err := c.es.Search(ctx, sensitiveWordsIndex, bytes.NewReader(body))
		if err != nil {
			return allowSensitiveCheckFailure(err)
		}
		if result == nil || result.TimedOut || result.Shards.Failed > 0 {
			return allowSensitiveCheckFailure(fmt.Errorf("elasticsearch returned an incomplete search response"))
		}
		for _, hit := range result.Hits.Hits {
			var source struct {
				Word string `json:"word"`
			}
			if err := json.Unmarshal(hit.Source, &source); err != nil {
				return allowSensitiveCheckFailure(fmt.Errorf("decode sensitive word: %w", err))
			}
			if source.Word != "" && strings.Contains(text, source.Word) {
				return true, nil
			}
		}
		if result.Hits.Total.Relation != "eq" {
			return incompleteSensitiveCheck("dictionary count is not exact")
		}
		if result.Hits.Total.Value > sensitiveWordsMaxWindow {
			return incompleteSensitiveCheck(fmt.Sprintf("dictionary has %d entries, exceeding the %d-entry result window", result.Hits.Total.Value, sensitiveWordsMaxWindow))
		}
		offset += len(result.Hits.Hits)
		if int64(offset) >= result.Hits.Total.Value {
			return false, nil
		}
		if len(result.Hits.Hits) == 0 {
			return incompleteSensitiveCheck("dictionary page ended before all entries were read")
		}
	}
	return incompleteSensitiveCheck("dictionary result window exhausted")
}

func allowSensitiveCheckFailure(err error) (bool, error) {
	// Preserve the existing infrastructure-failure policy for publishing/comments.
	slog.Warn("sensitive word check failed, allowing", "error", err)
	return false, nil
}

func incompleteSensitiveCheck(reason string) (bool, error) {
	err := fmt.Errorf("sensitive word check incomplete: %s", reason)
	slog.Warn("sensitive word check incomplete", "error", err)
	return false, err
}
