package main

import (
	"context"
	"fmt"
	"time"

	config "feedvideo.local/shared-config"
	"feedvideo.local/video/repo"
)

func prepareMediaJobIndexes(ctx context.Context, mode string, store repo.MediaJobStore) error {
	if mode != config.MediaTaskModeMongo {
		return nil
	}
	if store == nil {
		return fmt.Errorf("media job store unavailable")
	}
	indexCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return store.EnsureIndexes(indexCtx)
}
