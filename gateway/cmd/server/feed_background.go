package main

import (
	"context"
	"sync"

	feedservice "feedvideo.local/feed/service"
)

func startFeedBackground(ctx context.Context, wg *sync.WaitGroup, svc *feedservice.FeedService) error {
	if err := svc.StartBackground(ctx); err != nil {
		return err
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// This waits for local lifecycle completion, including owner cleanup.
		// The gateway's outer shutdown wait retains its existing time limit.
		_ = svc.WaitBackground(context.Background())
	}()
	return nil
}
