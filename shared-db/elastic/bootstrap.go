package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

var defaultIndexMappings = map[string]map[string]any{
	"sensitive_words": {
		"mappings": map[string]any{
			"properties": map[string]any{
				"word": map[string]any{"type": "text"},
			},
		},
	},
	"reports": {
		"mappings": map[string]any{
			"properties": map[string]any{
				"comment_id": map[string]any{"type": "keyword"},
				"reason":     map[string]any{"type": "keyword"},
				"timestamp":  map[string]any{"type": "date"},
			},
		},
	},
}

// DefaultSensitiveWords 是启动时写入敏感词索引的基础词库（MVP 示例级）。
var DefaultSensitiveWords = []string{
	"赌博", "暴力", "色情", "诈骗", "传销", "毒品", "刷单", "代购",
}

// EnsureDefaultIndexes creates the indexes the runtime depends on
// (sensitive_words for report checks, reports for moderation) if missing.
// It is idempotent and best-effort per index.
func (r *ESRepo) EnsureDefaultIndexes(ctx context.Context) error {
	if r == nil || r.Client == nil {
		return fmt.Errorf("elasticsearch client is nil")
	}
	var lastErr error
	for index, body := range defaultIndexMappings {
		exists, err := r.IndexExists(ctx, index)
		if err != nil {
			lastErr = err
			slog.Warn("ensure default index: exists check failed", "index", index, "error", err)
			continue
		}
		if exists {
			continue
		}
		raw, err := json.Marshal(body)
		if err != nil {
			lastErr = err
			continue
		}
		req := esapi.IndicesCreateRequest{
			Index: index,
			Body:  bytes.NewReader(raw),
		}
		res, err := req.Do(ctx, r.Client)
		if err != nil {
			lastErr = err
			slog.Warn("ensure default index: create failed", "index", index, "error", err)
			continue
		}
		res.Body.Close()
		// resource_already_exists_exception is fine (concurrent bootstrap).
		if res.IsError() && res.StatusCode != 400 {
			lastErr = fmt.Errorf("create index %s: %s", index, res.Status())
			continue
		}
		slog.Info("ensured default index", "index", index)
	}
	return lastErr
}

// SeedSensitiveWords 在 sensitive_words 索引为空时写入内置基础词库（幂等）。
func (r *ESRepo) SeedSensitiveWords(ctx context.Context, words []string) error {
	if r == nil || r.Client == nil || len(words) == 0 {
		return nil
	}
	n, err := r.Count(ctx, "sensitive_words", bytes.NewReader([]byte(`{"query":{"match_all":{}}}`)))
	if err != nil {
		return fmt.Errorf("count sensitive_words: %w", err)
	}
	if n > 0 {
		return nil
	}
	docs := make([]any, len(words))
	for i, w := range words {
		docs[i] = map[string]any{"word": w}
	}
	if _, err := r.BulkIndex(ctx, "sensitive_words", docs); err != nil {
		return fmt.Errorf("seed sensitive words: %w", err)
	}
	if err := r.Refresh(ctx, "sensitive_words"); err != nil {
		return fmt.Errorf("refresh sensitive words after seed: %w", err)
	}
	slog.Info("seeded sensitive words", "count", len(words))
	return nil
}
