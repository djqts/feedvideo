package service

import (
	"time"

	sharedconfig "feedvideo.local/shared-config"
)

// DefaultFeedWeights 统一 Feed 与推荐排序权重（热度/兴趣/时效）。
func DefaultFeedWeights() sharedconfig.FeedWeights {
	return sharedconfig.FeedWeights{Hotness: 0.5, Interest: 0.3, Recency: 0.2}
}

// ScoreVideo 统一打分：score = (w_h*hotness + w_i*interest + w_r*recency) / ΣW。
// hotness = hotScore/maxHot（maxHot<=0 记 0）；interest = |tags∩interests|/|interests|（空集记 0）；
// recency = 1/(1+ageHours)。Feed 候选池与 Recommend 召回共用该公式，保证排序语义一致。
func ScoreVideo(hotScore, maxHot float64, tags, interests []string, createdAt time.Time, w sharedconfig.FeedWeights, now time.Time) float64 {
	W := w.Hotness + w.Interest + w.Recency
	if W <= 0 {
		w = DefaultFeedWeights()
		W = 1
	}
	hotness := 0.0
	if maxHot > 0 {
		hotness = hotScore / maxHot
	}
	interest := 0.0
	if len(interests) > 0 {
		set := make(map[string]bool, len(interests))
		for _, t := range interests {
			set[t] = true
		}
		match := 0
		for _, tag := range tags {
			if set[tag] {
				match++
			}
		}
		interest = float64(match) / float64(len(interests))
	}
	recency := 1.0 / (1.0 + now.Sub(createdAt).Hours())
	return (w.Hotness*hotness + w.Interest*interest + w.Recency*recency) / W
}
