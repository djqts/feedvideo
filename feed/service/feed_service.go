package service

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	feedmodel "feedvideo.local/feed/model"
	feedrepo "feedvideo.local/feed/repo"
	sharedconfig "feedvideo.local/shared-config"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
)

// InterestProvider 由 user.UserService 实现（feed 只依赖接口，不 import user）
type InterestProvider interface {
	GetInterests(ctx context.Context, userID int64) ([]string, error)
}

// FollowProvider 由 user relation 缓存仓库实现
type FollowProvider interface {
	GetFollowingIDs(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
}

type FeedService struct {
	videoRepo  videorepo.VideoRepo
	interests  InterestProvider
	follows    FollowProvider
	feedRepo   *feedrepo.FeedRepo
	cfg        sharedconfig.FeedConfig
	candidates *candidateCache
	background *backgroundBuilds
}

func NewFeedService(
	videoRepo videorepo.VideoRepo,
	interests InterestProvider,
	follows FollowProvider,
	feedRepo *feedrepo.FeedRepo,
	cfg sharedconfig.FeedConfig,
) *FeedService {
	if cfg.BuildTimeoutMs <= 0 {
		cfg.BuildTimeoutMs = 500 // 防御性默认（code review Important #1）
	}
	if cfg.RecallSize <= 0 {
		cfg.RecallSize = 200
	}
	if cfg.RecallSize > 500 {
		cfg.RecallSize = 500
	}
	return &FeedService{
		videoRepo:  videoRepo,
		interests:  interests,
		follows:    follows,
		feedRepo:   feedRepo,
		cfg:        cfg,
		candidates: newCandidateCache(),
		background: newBackgroundBuilds(),
	}
}

// mediaVisibleFilter 只召回已审核且媒体处理成功的资产（P3.1）；
// 旧数据无 media_status 字段时兼容可见。
var mediaVisibleFilter = bson.M{
	"status": "approved",
	"$or": []bson.M{
		{"media_status": "succeeded"},
		{"media_status": ""},
		{"media_status": bson.M{"$exists": false}},
	},
}

// latestVisible 空榜/空池降级：按最新返回可见视频，避免首页空白（P1-1）。
func (s *FeedService) latestVisible(ctx context.Context, size int64) ([]*videomodel.Video, error) {
	if err := canceledRead(ctx, nil); err != nil {
		return nil, err
	}
	lister, ok := s.videoRepo.(videorepo.PublicVideoLister)
	if !ok {
		return nil, videorepo.ErrVideoReadUnavailable
	}
	videos, _, err := lister.ListPublicVideos(ctx, 1, size)
	if cancelErr := canceledRead(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, unavailable(err)
	}
	return videos, nil
}

// ——— 内部：候选池构建 ———

func (s *FeedService) buildPoolCandidates(ctx context.Context, uid string, uid64 int64, build *feedrepo.PoolBuild) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.interests == nil || s.feedRepo == nil || s.videoRepo == nil {
		return videorepo.ErrVideoReadUnavailable
	}
	interests, err := s.interests.GetInterests(ctx, uid64)
	if e := canceledRead(ctx, err); e != nil {
		return e
	}
	if err != nil {
		slog.Warn("get interests failed, use empty", "uid", uid, "error", err)
		interests = nil
	}
	recall := s.cfg.RecallSize
	if recall <= 0 {
		recall = 200
	}
	if recall > 500 {
		recall = 500
	}

	var candidates []*videomodel.Video
	if len(interests) > 0 {
		// The tag source's ordinary API caps one page at 200; hot candidates
		// can fill the rest of the independently bounded 500-entry pool.
		tagRecall := recall
		if tagRecall > 200 {
			tagRecall = 200
		}
		vids, _, err := s.videoRepo.SearchByTags(ctx, interests, 1, tagRecall)
		if e := canceledRead(ctx, err); e != nil {
			return e
		}
		if int64(len(vids)) > tagRecall {
			return videorepo.ErrVideoReadUnavailable
		}
		if err != nil {
			slog.Warn("search by tags failed", "uid", uid, "error", err) // 召回故障→纯 hot 候选
		} else {
			candidates = dedupVideos(vids)
		}
	}

	if int64(len(candidates)) < recall {
		exclude := make(map[string]bool, len(candidates))
		for _, v := range candidates {
			exclude[v.ID.Hex()] = true
		}
		fillIDs, err := s.feedRepo.HotFill(ctx, recall-int64(len(candidates)), exclude)
		if e := canceledRead(ctx, err); e != nil {
			return e
		}
		if err != nil {
			slog.Warn("hot fill failed during build", "uid", uid, "error", err)
		}
		if int64(len(fillIDs)) > recall-int64(len(candidates)) {
			return videorepo.ErrVideoReadUnavailable
		}
		if len(fillIDs) > 0 {
			videos, err := s.videoRepo.BatchGetVideos(ctx, fillIDs)
			if e := canceledRead(ctx, err); e != nil {
				return e
			}
			if len(videos) > len(fillIDs) {
				return videorepo.ErrVideoReadUnavailable
			}
			if err == nil {
				for _, id := range fillIDs {
					candidates = append(candidates, videos[id])
				}
			}
		}
	}
	candidates = dedupVideos(candidates)

	if len(candidates) == 0 {
		return errNoCandidates
	}

	ids := make([]string, len(candidates))
	for i, v := range candidates {
		ids[i] = v.ID.Hex()
	}
	hotScores, maxHot, err := s.feedRepo.BatchHotScores(ctx, ids)
	if e := canceledRead(ctx, err); e != nil {
		return e
	}
	if err != nil {
		slog.Warn("batch heat read failed, use zero heat", "error", err)
		hotScores, maxHot = nil, 0
	}
	scored := scoreVideos(candidates, hotScores, maxHot, interests, s.cfg.Weights, time.Now())
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.feedRepo.SetPool(ctx, uid, scored, build)
}

// ——— 内部辅助 ———

func (s *FeedService) videosByIDs(ctx context.Context, ids []string) []*videomodel.Video {
	if len(ids) == 0 {
		return nil
	}
	videoMap, err := s.videoRepo.BatchGetVideos(ctx, ids)
	if err != nil {
		slog.Warn("batch get videos failed", "error", err)
		return nil
	}
	out := make([]*videomodel.Video, 0, len(ids))
	for _, id := range ids {
		if v, ok := videoMap[id]; ok {
			out = append(out, v)
		}
	}
	return out
}

// toItems 统一用 BatchGetStats pipeline 合并 stats（缺失零值）
func (s *FeedService) toItems(ctx context.Context, videos []*videomodel.Video) ([]*feedmodel.FeedItem, error) {
	items, _, err := s.toItemsWithStats(ctx, videos)
	return items, err
}

// Statistics can degrade the response to zeros, but a failed read cannot
// authorize recommendation or cache writes.
func (s *FeedService) toItemsWithStats(ctx context.Context, videos []*videomodel.Video) ([]*feedmodel.FeedItem, bool, error) {
	if err := canceledRead(ctx, nil); err != nil {
		return nil, false, err
	}
	// Every candidate source, including cache and fallback paths, must still
	// satisfy moderation approval and media availability before publication.
	visible := make([]*videomodel.Video, 0, len(videos))
	for _, v := range videos {
		if !videomodel.IsPubliclyVisible(v) {
			continue
		}
		visible = append(visible, v)
	}
	items := make([]*feedmodel.FeedItem, 0, len(visible))
	if len(visible) == 0 {
		return items, true, nil
	}
	ids := make([]string, 0, len(visible))
	for _, v := range visible {
		ids = append(ids, v.ID.Hex())
	}
	stats, err := s.videoRepo.BatchGetStats(ctx, ids)
	if cancelErr := canceledRead(ctx, err); cancelErr != nil {
		return nil, false, cancelErr
	}
	statsOK := err == nil
	if err != nil {
		slog.Warn("batch get stats failed, use zero stats", "error", err)
		stats = map[string]*videomodel.VideoStats{}
	}
	for _, v := range visible {
		id := v.ID.Hex()
		st := stats[id]
		if st == nil {
			st = &videomodel.VideoStats{VideoID: id}
		}
		items = append(items, &feedmodel.FeedItem{Video: v, Stats: st})
	}
	if err := canceledRead(ctx, nil); err != nil {
		return nil, false, err
	}
	return items, statsOK, nil
}

// dedupVideos 请求内按视频 ID 去重，保序
func dedupVideos(videos []*videomodel.Video) []*videomodel.Video {
	seen := make(map[string]bool, len(videos))
	out := make([]*videomodel.Video, 0, len(videos))
	for _, v := range videos {
		if v == nil {
			continue
		}
		id := v.ID.Hex()
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, v)
	}
	return out
}
