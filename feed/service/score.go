package service

import (
	"errors"
	"time"

	feedrepo "feedvideo.local/feed/repo"
	sharedconfig "feedvideo.local/shared-config"
	videomodel "feedvideo.local/video/model"
	videoService "feedvideo.local/video/service"
)

var errNoCandidates = errors.New("feed: no candidates to build pool")

// scoreVideos 纯函数打分：委托 video/service.ScoreVideo，与 Recommend 共用同一公式与权重。
func scoreVideos(
	videos []*videomodel.Video,
	hotScores map[string]float64,
	maxHot float64,
	interests []string,
	w sharedconfig.FeedWeights,
	now time.Time,
) []feedrepo.ScoredVideo {
	out := make([]feedrepo.ScoredVideo, 0, len(videos))
	for _, v := range videos {
		id := v.ID.Hex()
		score := videoService.ScoreVideo(hotScores[id], maxHot, v.Tags, interests, v.CreatedAt, w, now)
		out = append(out, feedrepo.ScoredVideo{ID: id, Score: score})
	}
	return out
}
