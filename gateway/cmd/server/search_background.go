package main

import (
	"context"
	"sync"

	searchservice "feedvideo.local/search/service"
	"feedvideo.local/shared-auth/middlewares"
)

// Start before accepting HTTP traffic; wait for actual consumer exit on shutdown.
func startSearchBackground(ctx context.Context, wg *sync.WaitGroup, svc *searchservice.SearchService, metrics *middlewares.Metrics) error {
	if err := svc.StartBackground(ctx); err != nil {
		return err
	}
	metrics.SetSearchBackgroundReader(func() middlewares.SearchBackgroundSnapshot {
		stats := svc.BackgroundStats()
		return middlewares.SearchBackgroundSnapshot{
			Running: stats.Running, Queued: int64(stats.Queued), Active: int64(stats.Active),
			Accepted: stats.Accepted, Completed: stats.Completed, Failed: stats.Failed,
			Dropped: stats.Dropped, Deduplicated: stats.Deduplicated,
			MissingIdentity: stats.MissingIdentity, DedupCapacityDropped: stats.DedupCapacityDropped,
		}
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = svc.WaitBackground(context.Background())
	}()
	return nil
}
