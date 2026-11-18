package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	feedmodel "feedvideo.local/feed/model"
	feedrepo "feedvideo.local/feed/repo"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
)

func unavailable(err error) error {
	if err == nil {
		return videorepo.ErrVideoReadUnavailable
	}
	return fmt.Errorf("%w: %w", videorepo.ErrVideoReadUnavailable, err)
}
func canceledRead(ctx context.Context, err error) error {
	if e := ctx.Err(); e != nil {
		return unavailable(e)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return unavailable(err)
	}
	return nil
}
func (s *FeedService) requestContext(ctx context.Context, size int64) (context.Context, context.CancelFunc, error) {
	if size < 1 || size > 50 {
		return nil, nil, videorepo.ErrVideoReadInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if err := canceledRead(ctx, nil); err != nil {
		cancel()
		return nil, nil, err
	}
	if s == nil || s.videoRepo == nil {
		cancel()
		return nil, nil, videorepo.ErrVideoReadUnavailable
	}
	return ctx, cancel, nil
}
func feedResponse(items []*feedmodel.FeedItem, size int64) *feedmodel.FeedResp {
	return &feedmodel.FeedResp{List: items, HasMore: int64(len(items)) >= size}
}
func (s *FeedService) Following(ctx context.Context, uid string, size int64) (*feedmodel.FeedResp, error) {
	ctx, cancel, err := s.requestContext(ctx, size)
	if err != nil {
		return nil, err
	}
	defer cancel()
	uid64, err := strconv.ParseInt(uid, 10, 64)
	if err != nil || uid64 <= 0 {
		return s.Hot(ctx, size)
	}
	if s.follows == nil {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	ids, err := s.follows.GetFollowingIDs(ctx, uid64, 0, 100)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	if err != nil {
		return s.Hot(ctx, size)
	}
	if len(ids) > 100 {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	if len(ids) == 0 {
		return feedResponse([]*feedmodel.FeedItem{}, size), nil
	}
	authors := make([]string, 0, len(ids))
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		author := strconv.FormatInt(id, 10)
		if !allowed[author] {
			authors = append(authors, author)
			allowed[author] = true
		}
	}
	videos, err := s.videoRepo.ListByAuthors(ctx, authors, 1, size)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	if err != nil {
		return s.Hot(ctx, size)
	}
	videos, err = s.currentHotVideos(ctx, videos, size)
	if err != nil {
		return nil, err
	}
	out := videos[:0]
	for _, v := range videos {
		if allowed[v.AuthorID] {
			out = append(out, v)
		}
	}
	items, err := s.toItems(ctx, out)
	if err != nil {
		return nil, err
	}
	return feedResponse(items, size), nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validCoordinates(lng, lat float64) bool {
	return finite(lng) && finite(lat) && lng >= -180 && lng <= 180 && lat >= -90 && lat <= 90
}
func withinRadius(v *videomodel.Video, lng, lat, radius float64) bool {
	if v.Location == nil || v.Location.Type != "Point" || len(v.Location.Coordinates) != 2 {
		return false
	}
	p := v.Location.Coordinates
	if !validCoordinates(p[0], p[1]) {
		return false
	}
	dlat := (p[1] - lat) * math.Pi / 180
	dlng := (p[0] - lng) * math.Pi / 180
	a := math.Pow(math.Sin(dlat/2), 2) + math.Cos(lat*math.Pi/180)*math.Cos(p[1]*math.Pi/180)*math.Pow(math.Sin(dlng/2), 2)
	a = math.Min(1, math.Max(0, a))
	return 2*6378100*math.Asin(math.Sqrt(a)) <= radius
}
func (s *FeedService) Nearby(ctx context.Context, lng, lat, radius float64, size int64) (*feedmodel.FeedResp, error) {
	if !validCoordinates(lng, lat) || !finite(radius) || radius < 1 || radius > 100000 {
		return nil, videorepo.ErrVideoReadInvalid
	}
	ctx, cancel, err := s.requestContext(ctx, size)
	if err != nil {
		return nil, err
	}
	defer cancel()
	videos, err := s.videoRepo.GeoSearch(ctx, lng, lat, radius, size)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	geo := err == nil && len(videos) > 0
	if !geo {
		videos, err = s.latestVisible(ctx, size)
		if err != nil {
			return nil, err
		}
	}
	videos, err = s.currentHotVideos(ctx, videos, size)
	if err != nil {
		return nil, err
	}
	if geo {
		out := videos[:0]
		for _, v := range videos {
			if withinRadius(v, lng, lat, radius) {
				out = append(out, v)
			}
		}
		videos = out
	}
	items, err := s.toItems(ctx, videos)
	if err != nil {
		return nil, err
	}
	return feedResponse(items, size), nil
}
func (s *FeedService) latestVisibleResp(ctx context.Context, uid string, size int64) (*feedmodel.FeedResp, error) {
	key := candidateKey("recommend-latest", uid, size)
	ids := s.candidates.get(key)
	var videos []*videomodel.Video
	var err error
	if len(ids) > 0 {
		videos, err = s.currentIDs(ctx, ids, size)
	} else {
		videos, err = s.latestVisible(ctx, size)
		if err == nil {
			videos, err = s.currentHotVideos(ctx, videos, size)
		}
	}
	if err != nil {
		return nil, err
	}
	items, statsOK, err := s.toItemsWithStats(ctx, videos)
	if err != nil {
		return nil, err
	}
	if e := canceledRead(ctx, nil); e != nil {
		return nil, e
	}
	if statsOK && len(ids) == 0 && len(items) > 0 {
		s.candidates.set(key, itemIDs(items))
	}
	return feedResponse(items, size), nil
}
func itemIDs(items []*feedmodel.FeedItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Video.ID.Hex())
	}
	return ids
}
func (s *FeedService) Recommend(ctx context.Context, uid string, size int64) (*feedmodel.FeedResp, error) {
	ctx, cancel, err := s.requestContext(ctx, size)
	if err != nil {
		return nil, err
	}
	defer cancel()
	uid64, err := strconv.ParseInt(uid, 10, 64)
	if err != nil || uid64 <= 0 {
		return s.Hot(ctx, size)
	}
	if s.feedRepo == nil {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	fallback := func(err error) (*feedmodel.FeedResp, error) {
		if e := canceledRead(ctx, err); e != nil {
			return nil, e
		}
		return s.latestVisibleResp(ctx, uid, size)
	}
	state, err := s.feedRepo.PoolState(ctx, uid)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	if err != nil {
		return fallback(err)
	}
	switch state {
	case feedrepo.PoolStale:
		cool, err := s.feedRepo.InRetryCooldown(ctx, uid)
		if e := canceledRead(ctx, err); e != nil {
			return nil, e
		}
		if err != nil {
			return fallback(err)
		}
		if !cool {
			token, lockErr := s.feedRepo.AcquireBuildLock(ctx, uid)
			if lockErr != nil {
				s.releaseBuild(ctx, uid, token)
			}
			if e := canceledRead(ctx, lockErr); e != nil {
				if lockErr == nil {
					s.releaseBuild(ctx, uid, token)
				}
				return nil, e
			}
			if lockErr == nil && token != "" {
				buildErr := s.startStaleBuild(ctx, uid, uid64, token)
				if e := canceledRead(ctx, buildErr); e != nil {
					return nil, e
				}
			}
		}
	case feedrepo.PoolMissing:
		miss, err := s.feedRepo.IsPoolMiss(ctx, uid)
		if e := canceledRead(ctx, err); e != nil {
			return nil, e
		}
		if err != nil {
			return fallback(err)
		}
		if miss {
			return s.Hot(ctx, size)
		}
		token, err := s.feedRepo.AcquireBuildLock(ctx, uid)
		if err != nil {
			s.releaseBuild(ctx, uid, token)
		}
		if e := canceledRead(ctx, err); e != nil {
			if err == nil {
				s.releaseBuild(ctx, uid, token)
			}
			return nil, e
		}
		if err != nil || token == "" {
			return s.Hot(ctx, size)
		}
		build, beginErr := s.feedRepo.BeginPoolBuild(ctx, uid, token)
		if beginErr != nil {
			s.releaseBuild(ctx, uid, token)
			if e := canceledRead(ctx, beginErr); e != nil {
				return nil, e
			}
			return s.Hot(ctx, size)
		}
		bctx, bcancel := context.WithTimeout(ctx, time.Duration(s.cfg.BuildTimeoutMs)*time.Millisecond)
		buildErr := s.buildPool(bctx, uid, uid64, build)
		bcancel()
		// A builder has its own shorter budget; only the request's cancellation
		// terminates this established miss/Hot fallback branch.
		var writeErr error
		if buildErr != nil && ctx.Err() == nil && !errors.Is(buildErr, feedrepo.ErrPoolBuildRejected) {
			writeErr = s.feedRepo.SetPoolMiss(ctx, uid, build)
		}
		s.releaseBuild(ctx, uid, token)
		if e := canceledRead(ctx, writeErr); e != nil {
			return nil, e
		}
		if buildErr != nil {
			return s.Hot(ctx, size)
		}
	}
	seen, err := s.feedRepo.GetRecommended(ctx, uid)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	if err != nil {
		return fallback(err)
	}
	card, err := s.feedRepo.PoolCard(ctx, uid)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	if err != nil {
		return fallback(err)
	}
	width := size * 3
	var picked []string
	pickedSet := map[string]bool{}
	for {
		if e := canceledRead(ctx, nil); e != nil {
			return nil, e
		}
		window, err := s.feedRepo.PoolWindow(ctx, uid, width-1)
		if e := canceledRead(ctx, err); e != nil {
			return nil, e
		}
		if err != nil {
			return fallback(err)
		}
		picked = nil
		pickedSet = map[string]bool{}
		for _, id := range window {
			if seen[id] || pickedSet[id] {
				continue
			}
			picked = append(picked, id)
			pickedSet[id] = true
			if int64(len(picked)) >= size {
				break
			}
		}
		if int64(len(picked)) >= size || int64(len(window)) >= card || width >= 500 {
			break
		}
		width *= 2
		if width > 500 {
			width = 500
		}
	}
	if int64(len(picked)) < size {
		exclude := make(map[string]bool, len(seen)+len(pickedSet))
		for id := range seen {
			exclude[id] = true
		}
		for id := range pickedSet {
			exclude[id] = true
		}
		fill, err := s.feedRepo.HotFill(ctx, size-int64(len(picked)), exclude)
		if e := canceledRead(ctx, err); e != nil {
			return nil, e
		}
		if err != nil {
			return fallback(err)
		}
		for _, id := range fill {
			if !pickedSet[id] {
				picked = append(picked, id)
				pickedSet[id] = true
			}
		}
	}
	videos, err := s.currentIDs(ctx, picked, size)
	if err != nil {
		return nil, err
	}
	items, statsOK, err := s.toItemsWithStats(ctx, videos)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return s.latestVisibleResp(ctx, uid, size)
	}
	actual := itemIDs(items)
	combined := make(map[string]bool, len(seen)+len(actual))
	for id := range seen {
		combined[id] = true
	}
	for _, id := range actual {
		combined[id] = true
	}
	hasUnseen, err := s.feedRepo.PoolHasUnseen(ctx, uid, combined)
	if e := canceledRead(ctx, err); e != nil {
		return nil, e
	}
	if err != nil {
		return fallback(err)
	}
	if statsOK {
		markErr := s.feedRepo.MarkRecommended(ctx, uid, actual)
		if e := canceledRead(ctx, markErr); e != nil {
			return nil, e
		}
		if markErr != nil {
			slog.Warn("mark recommended failed", "uid", uid, "error", markErr)
		}
	}
	if e := canceledRead(ctx, nil); e != nil {
		return nil, e
	}
	return &feedmodel.FeedResp{List: items, HasMore: int64(len(picked)) >= size && hasUnseen}, nil
}

// Cleanup is independent of the request/builder cancellation, but still bounded.
func (s *FeedService) releaseBuild(ctx context.Context, uid, token string) {
	if token == "" {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := s.feedRepo.ReleaseBuildLock(cleanup, uid, token); err != nil {
		slog.Warn("release feed build failed", "uid", uid, "error", err)
	}
}
