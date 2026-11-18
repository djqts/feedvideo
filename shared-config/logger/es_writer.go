package logger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ESBulkIndexer ES 批量写入接口，由 elastic.ESRepo.BulkIndex 满足
type ESBulkIndexer func(ctx context.Context, index string, docs []any) error

// ESConfig ES 日志写入配置
type ESConfig struct {
	Enabled    bool
	Index      string        // 索引名，如 "app-logs"
	FlushSize  int           // 批量大小，默认 100
	FlushEvery time.Duration // 批量间隔，默认 5s
	Indexer    ESBulkIndexer // 批量写入函数
}

// ESWriter 实现 io.Writer，将日志行批量写入 Elasticsearch
type ESWriter struct {
	cfg     ESConfig
	buf     bytes.Buffer
	scanner *bufio.Scanner
	mu      sync.Mutex
	docs    []any
	ticker  *time.Ticker
	done    chan struct{}
}

func NewESWriter(cfg ESConfig) *ESWriter {
	if cfg.FlushSize <= 0 {
		cfg.FlushSize = 100
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 5 * time.Second
	}

	w := &ESWriter{
		cfg:    cfg,
		ticker: time.NewTicker(cfg.FlushEvery),
		done:   make(chan struct{}),
		docs:   make([]any, 0, cfg.FlushSize),
	}

	// 后台定时刷新
	go func() {
		for {
			select {
			case <-w.ticker.C:
				w.flush()
			case <-w.done:
				w.ticker.Stop()
				w.flush()
				return
			}
		}
	}()

	return w
}

func (w *ESWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n = len(p)

	// 按行分割，每行是一个 JSON 日志条目
	lines := bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(line, &doc); err != nil {
			// 非 JSON 行，包装后写入
			doc = map[string]any{
				"message":  string(line),
				"raw":      true,
				"@timestamp": time.Now(),
			}
		}
		// 统一追加时间戳
		if _, ok := doc["@timestamp"]; !ok {
			doc["@timestamp"] = time.Now()
		}
		w.docs = append(w.docs, doc)

		if len(w.docs) >= w.cfg.FlushSize {
			w.flushLocked()
		}
	}

	return n, nil
}

func (w *ESWriter) Close() error {
	close(w.done)
	return nil
}

func (w *ESWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
}

func (w *ESWriter) flushLocked() {
	if len(w.docs) == 0 || w.cfg.Indexer == nil {
		return
	}

	index := w.cfg.Index
	if index == "" {
		index = "app-logs"
	}
	// 按天分索引
	index = fmt.Sprintf("%s-%s", index, time.Now().Format("2006-01-02"))

	docs := make([]any, len(w.docs))
	copy(docs, w.docs)
	w.docs = w.docs[:0]

	// 不阻塞日志写入，异步发送到 ES
	go func() {
		if err := w.cfg.Indexer(context.Background(), index, docs); err != nil {
			slog.Warn("es log writer bulk index failed", "count", len(docs), "error", err)
		}
	}()
}
