package main

import (
	"context"
	"errors"
	"feedvideo.local/user/service"
	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"math"
	"time"
)

type watchIndexStore interface{ EnsureIndexes(context.Context) error }

func prepareWatchIndexes(ctx context.Context, store watchIndexStore) error {
	if store == nil {
		return errors.New("watch indexes unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return store.EnsureIndexes(ctx)
}

type watchCurrentReader interface {
	GetCurrentByID(context.Context, string) (*model.Video, error)
}

func watchMediaSource(reader watchCurrentReader) service.WatchSource {
	return func(ctx context.Context, id string) (service.WatchMedia, error) {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil || reader == nil {
			return service.WatchMedia{}, errors.New("watch media unavailable")
		}
		video, err := reader.GetCurrentByID(ctx, id)
		if errors.Is(err, repo.ErrVideoNotFound) {
			return service.WatchMedia{}, nil
		}
		if err != nil {
			return service.WatchMedia{}, err
		}
		if ctx.Err() != nil {
			return service.WatchMedia{}, ctx.Err()
		}
		if video == nil || video.ID != oid {
			return service.WatchMedia{}, errors.New("watch media identity mismatch")
		}
		if !model.IsPubliclyVisible(video) {
			return service.WatchMedia{}, nil
		}
		if math.IsNaN(video.Duration) || math.IsInf(video.Duration, 0) || video.Duration <= 0 || video.Duration > 86400 {
			return service.WatchMedia{}, errors.New("watch duration unavailable")
		}
		return service.WatchMedia{Public: true, ContentVersion: video.ContentVersion, DurationMS: int64(math.Round(video.Duration * 1000))}, nil
	}
}
