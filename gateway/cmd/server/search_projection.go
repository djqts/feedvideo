package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"feedvideo.local/gateway/internal/scheduler"
	searchService "feedvideo.local/search/service"
	"feedvideo.local/shared-auth/middlewares"
	config "feedvideo.local/shared-config"
	"feedvideo.local/video/repo"
	"github.com/elastic/go-elasticsearch/v8"
)

type searchProjectionSource interface {
	ConfigureSearchProjection(enabled bool, index string) error
}

type searchProjectionRuntime struct {
	worker *scheduler.SearchProjectionWorker
	index  string
}

// Stores and sinks are constructed lazily so legacy has no lease-worker side effects.
func prepareSearchProjection(ctx context.Context, mode, index string, source searchProjectionSource,
	newStore func() repo.SearchProjectionStore, newSink func(string) scheduler.SearchProjectionSink,
) (*searchProjectionRuntime, error) {
	mode, err := config.ParseSearchProjectionMode(mode)
	if err != nil {
		return nil, err
	}
	index, err = config.ParseSearchProjectionIndex(index)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, fmt.Errorf("search projection source unavailable")
	}
	indexCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := indexCtx.Err(); err != nil {
		return nil, err
	}
	if err := source.ConfigureSearchProjection(mode == config.SearchProjectionModeMongo, index); err != nil {
		return nil, fmt.Errorf("search projection source configuration: %w", err)
	}
	if mode == config.SearchProjectionModeLegacy {
		return &searchProjectionRuntime{}, nil
	}
	if newStore == nil {
		return nil, fmt.Errorf("search projection store factory unavailable")
	}
	store := newStore()
	if store == nil {
		return nil, fmt.Errorf("search projection store unavailable")
	}
	if err := indexCtx.Err(); err != nil {
		return nil, err
	}
	if err := store.EnsureIndexes(indexCtx); err != nil {
		return nil, fmt.Errorf("search projection indexes incomplete: %w", err)
	}
	if err := indexCtx.Err(); err != nil {
		return nil, err
	}
	if newSink == nil {
		return nil, fmt.Errorf("search projection sink factory unavailable")
	}
	sink := newSink(index)
	if sink == nil {
		return nil, fmt.Errorf("search projection sink unavailable")
	}
	if err := indexCtx.Err(); err != nil {
		return nil, err
	}
	return &searchProjectionRuntime{worker: scheduler.NewSearchProjectionWorker(store, sink), index: index}, nil
}

// Mongo constructs a retryable client without any startup Ping or ES index writes.
func initializeSearchProjectionES(mode string, addresses []string,
	legacyInit, projectionInit func() (*elasticsearch.Client, error),
) (*elasticsearch.Client, error) {
	mode, err := config.ParseSearchProjectionMode(mode)
	if err != nil {
		return nil, err
	}
	configured := len(addresses) > 0 && strings.TrimSpace(addresses[0]) != ""
	if !configured {
		if mode == config.SearchProjectionModeMongo {
			return nil, fmt.Errorf("search projection requires Elasticsearch addresses")
		}
		return nil, nil
	}
	initialize := legacyInit
	if mode == config.SearchProjectionModeMongo {
		initialize = projectionInit
	}
	if initialize == nil {
		return nil, fmt.Errorf("Elasticsearch initializer unavailable")
	}
	client, err := initialize()
	if err != nil {
		return nil, err
	}
	if client == nil && mode == config.SearchProjectionModeMongo {
		return nil, fmt.Errorf("search projection Elasticsearch client unavailable")
	}
	return client, nil
}

func (p *searchProjectionRuntime) configureCache(service *searchService.SearchService) {
	if p != nil && p.index != "" {
		service.ConfigureCacheNamespace(p.index)
	}
}

func (p *searchProjectionRuntime) start(ctx context.Context, wg *sync.WaitGroup) {
	if p == nil || p.worker == nil {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.worker.Run(ctx)
	}()
}

// Each invocation reads Stats and the clock once; Handler owns the returned value.
func (p *searchProjectionRuntime) metricsSnapshot() middlewares.SearchProjectionSnapshot {
	var stats scheduler.SearchProjectionStats
	if p != nil && p.worker != nil {
		stats = p.worker.Stats()
	}
	return searchProjectionMetricsSnapshot(stats, time.Now())
}

func searchProjectionMetricsSnapshot(stats scheduler.SearchProjectionStats, now time.Time) middlewares.SearchProjectionSnapshot {
	snapshot := middlewares.SearchProjectionSnapshot{
		Pending: -1, OldestSeconds: -1, PendingKnown: stats.PendingKnown,
		Succeeded: stats.Succeeded, Failed: stats.Failed, Conflicts: stats.Conflicts, Expired: stats.Expired,
	}
	if !stats.PendingKnown {
		return snapshot
	}
	snapshot.Pending = stats.Pending
	if stats.Pending == 0 {
		snapshot.OldestSeconds = 0
	} else if !stats.Oldest.IsZero() {
		age := now.Sub(stats.Oldest)
		if age < 0 {
			age = 0
		}
		snapshot.OldestSeconds = int64(age / time.Second)
	}
	return snapshot
}
