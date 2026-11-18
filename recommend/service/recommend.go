package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	"feedvideo.local/shared-db/cache"
	videomodel "feedvideo.local/video/model"
	videoService "feedvideo.local/video/service"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type RecommendService struct {
	db           *mongo.Database
	rdb          *cache.RedisClient
	videoColl    *mongo.Collection
	behaviorColl *mongo.Collection
}

func New(db *mongo.Database, rdb *cache.RedisClient) *RecommendService {
	return &RecommendService{
		db:           db,
		rdb:          rdb,
		videoColl:    db.Collection("videos"),
		behaviorColl: db.Collection("behavior_logs"),
	}
}

// ForYou returns personalized recommendations.
func (s *RecommendService) ForYou(ctx context.Context, userID string, size int64) ([]*videomodel.Video, error) {
	seenIDs := s.getSeenIDs(ctx, userID, 100)
	if len(seenIDs) == 0 {
		return s.coldStart(ctx, size)
	}

	// Parallel recalls with independent timeouts
	type recallResult struct {
		videos []string
		source string
	}
	results := make(chan recallResult, 3)

	// Collaborative (500ms timeout)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		ids, err := s.collaborativeRecall(ctx, userID, seenIDs, size*2)
		if err != nil {
			slog.Warn("collaborative recall failed", "error", err)
			return
		}
		results <- recallResult{ids, "collab"}
	}()

	// Content (300ms timeout)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		ids, err := s.contentRecall(ctx, userID, seenIDs, size*2)
		if err != nil {
			slog.Warn("content recall failed", "error", err)
			return
		}
		results <- recallResult{ids, "content"}
	}()

	// Hot (300ms timeout)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		ids, err := s.hotRecall(ctx, size*2)
		if err != nil {
			slog.Warn("hot recall failed", "error", err)
			return
		}
		results <- recallResult{ids, "hot"}
	}()

	// Collect results (timeout after 1s total)
	var allIDs []string
	seen := make(map[string]bool)
	for _, id := range seenIDs {
		seen[id] = true
	}

	deadline := time.After(1 * time.Second)
	collected := 0
	for collected < 3 {
		select {
		case r := <-results:
			collected++
			for _, id := range r.videos {
				if !seen[id] {
					allIDs = append(allIDs, id)
					seen[id] = true
				}
			}
		case <-deadline:
			collected = 3 // break
		case <-ctx.Done():
			collected = 3 // break
		}
	}

	// Fallback: if nothing found, use hot
	if len(allIDs) == 0 {
		return s.coldStart(ctx, size)
	}

	return s.fetchAndScore(ctx, allIDs, size)
}

// Similar returns videos with similar tags.
func (s *RecommendService) Similar(ctx context.Context, videoID, userID string, size int64) ([]*videomodel.Video, error) {
	// Get target video tags
	objID, err := primitive.ObjectIDFromHex(videoID)
	if err != nil {
		return nil, err
	}
	var target struct {
		Tags []string `bson:"tags"`
	}
	if err := s.videoColl.FindOne(ctx, bson.M{"_id": objID}).Decode(&target); err != nil {
		return nil, err
	}
	if len(target.Tags) == 0 {
		return nil, nil
	}

	// Find videos with matching tags (exclude self)；与 Feed 对齐可见性（仅已审核且媒体成功的视频）
	filter := bson.M{
		"_id":    bson.M{"$ne": objID},
		"tags":   bson.M{"$in": target.Tags},
		"status": "approved",
		"$or": []bson.M{
			{"media_status": "succeeded"},
			{"media_status": bson.M{"$exists": false}},
		},
	}
	if userID != "" {
		seenIDs := s.getSeenIDs(ctx, userID, 100)
		if len(seenIDs) > 0 {
			var exclude []primitive.ObjectID
			for _, id := range seenIDs {
				if oid, err := primitive.ObjectIDFromHex(id); err == nil {
					exclude = append(exclude, oid)
				}
			}
			if len(exclude) > 0 {
				filter["_id"].(bson.M)["$nin"] = exclude
			}
		}
	}

	cursor, err := s.videoColl.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var videos []*videomodel.Video
	if err := cursor.All(ctx, &videos); err != nil {
		return nil, err
	}

	// Sort by tag overlap + heat
	sort.Slice(videos, func(i, j int) bool {
		oi := tagOverlap(target.Tags, videos[i].Tags)
		oj := tagOverlap(target.Tags, videos[j].Tags)
		return oi > oj
	})

	if int64(len(videos)) > size {
		videos = videos[:size]
	}
	return videos, nil
}

// ——— Private recall methods ———

func (s *RecommendService) collaborativeRecall(ctx context.Context, userID string, seenIDs []string, limit int64) ([]string, error) {
	// Step 1: Get user's high-value videos (like/collect only)
	userVidFilter := bson.M{
		"user_id": userID,
		"action":  bson.M{"$in": []string{"like", "collect"}},
	}
	uvCursor, err := s.behaviorColl.Find(ctx, userVidFilter)
	if err != nil {
		return nil, err
	}
	defer uvCursor.Close(ctx)

	var userVideos []string
	seen := make(map[string]bool)
	for _, id := range seenIDs {
		seen[id] = true
	}
	for uvCursor.Next(ctx) {
		var log struct {
			TargetID string `bson:"target_id"`
		}
		uvCursor.Decode(&log)
		if log.TargetID != "" && !seen[log.TargetID] {
			userVideos = append(userVideos, log.TargetID)
		}
	}
	if len(userVideos) == 0 {
		return nil, nil
	}

	// Step 2: Find similar users via co-occurrence
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"target_id": bson.M{"$in": userVideos},
			"user_id":   bson.M{"$ne": userID},
			"action":    bson.M{"$in": []string{"like", "collect"}},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":          "$user_id",
			"common_count": bson.M{"$sum": 1},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "common_count", Value: -1}}}},
		{{Key: "$limit", Value: 50}},
	}
	suCursor, err := s.behaviorColl.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer suCursor.Close(ctx)

	var similarUsers []string
	for suCursor.Next(ctx) {
		var r struct {
			ID string `bson:"_id"`
		}
		suCursor.Decode(&r)
		if r.ID != "" {
			similarUsers = append(similarUsers, r.ID)
		}
	}
	if len(similarUsers) == 0 {
		return nil, nil
	}

	// Step 3: Get videos from similar users (exclude seen)
	vidFilter := bson.M{
		"user_id":   bson.M{"$in": similarUsers},
		"target_id": bson.M{"$nin": seenIDs},
		"action":    bson.M{"$in": []string{"like", "collect"}},
	}
	vcCursor, err := s.behaviorColl.Find(ctx, vidFilter)
	if err != nil {
		return nil, err
	}
	defer vcCursor.Close(ctx)

	var candidates []string
	candSeen := make(map[string]bool)
	for _, id := range seenIDs {
		candSeen[id] = true
	}
	for vcCursor.Next(ctx) {
		var log struct {
			TargetID string `bson:"target_id"`
		}
		vcCursor.Decode(&log)
		if log.TargetID != "" && !candSeen[log.TargetID] {
			candidates = append(candidates, log.TargetID)
			candSeen[log.TargetID] = true
			if int64(len(candidates)) >= limit {
				break
			}
		}
	}
	return candidates, nil
}

func (s *RecommendService) contentRecall(ctx context.Context, userID string, seenIDs []string, limit int64) ([]string, error) {
	// Get recent tags (last 7 days)
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	tagFilter := bson.M{
		"user_id":     userID,
		"action":      bson.M{"$in": []string{"like", "collect", "play"}},
		"timestamp":   bson.M{"$gte": sevenDaysAgo},
		"target_type": "video",
	}
	tCursor, err := s.behaviorColl.Find(ctx, tagFilter)
	if err != nil {
		return nil, err
	}
	defer tCursor.Close(ctx)

	var videoIDs []string
	for tCursor.Next(ctx) {
		var log struct {
			TargetID string `bson:"target_id"`
		}
		tCursor.Decode(&log)
		if log.TargetID != "" {
			videoIDs = append(videoIDs, log.TargetID)
		}
	}
	if len(videoIDs) == 0 {
		return nil, nil
	}

	// Get tags from those videos
	var oids []primitive.ObjectID
	for _, id := range videoIDs {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			oids = append(oids, oid)
		}
	}
	if len(oids) == 0 {
		return nil, nil
	}

	vCursor, err := s.videoColl.Find(ctx, bson.M{"_id": bson.M{"$in": oids}})
	if err != nil {
		return nil, err
	}
	defer vCursor.Close(ctx)

	tagCount := make(map[string]int)
	for vCursor.Next(ctx) {
		var v struct {
			Tags []string `bson:"tags"`
		}
		vCursor.Decode(&v)
		for _, t := range v.Tags {
			tagCount[t]++
		}
	}

	// Top tags
	type tagScore struct {
		tag   string
		count int
	}
	var topTags []tagScore
	for t, c := range tagCount {
		topTags = append(topTags, tagScore{t, c})
	}
	sort.Slice(topTags, func(i, j int) bool { return topTags[i].count > topTags[j].count })
	if len(topTags) > 5 {
		topTags = topTags[:5]
	}
	var queryTags []string
	for _, t := range topTags {
		queryTags = append(queryTags, t.tag)
	}

	// Exclude seen
	var excludeOids []primitive.ObjectID
	for _, id := range seenIDs {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			excludeOids = append(excludeOids, oid)
		}
	}

	// Find videos by tags
	filter := bson.M{"tags": bson.M{"$in": queryTags}}
	if len(excludeOids) > 0 {
		filter["_id"] = bson.M{"$nin": excludeOids}
	}
	cursor, err := s.videoColl.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var candidates []*videomodel.Video
	if err := cursor.All(ctx, &candidates); err != nil {
		return nil, err
	}

	// Sort by tag overlap + heat
	sort.Slice(candidates, func(i, j int) bool {
		si := float64(tagOverlap(queryTags, candidates[i].Tags)) / float64(len(queryTags)) * 10
		sj := float64(tagOverlap(queryTags, candidates[j].Tags)) / float64(len(queryTags)) * 10
		return si > sj
	})

	var ids []string
	for _, v := range candidates {
		ids = append(ids, v.ID.Hex())
		if int64(len(ids)) >= limit {
			break
		}
	}
	return ids, nil
}

func (s *RecommendService) hotRecall(ctx context.Context, limit int64) ([]string, error) {
	var ids []string
	// Redis hot videos (top 100)
	members, _ := s.rdb.ZRevRange(ctx, cache.HotVideosKey, 0, 99)
	ids = append(ids, members...)

	// MongoDB newest 20 for freshness
	cursor, err := s.videoColl.Find(ctx, bson.M{}, nil)
	if err == nil {
		defer cursor.Close(ctx)
		var fresh []*videomodel.Video
		cursor.All(ctx, &fresh)
		sort.Slice(fresh, func(i, j int) bool {
			return fresh[i].CreatedAt.After(fresh[j].CreatedAt)
		})
		for i, v := range fresh {
			if i >= 20 {
				break
			}
			ids = append(ids, v.ID.Hex())
		}
	}

	// Dedup
	seen := make(map[string]bool)
	var result []string
	for _, id := range ids {
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
			if int64(len(result)) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (s *RecommendService) coldStart(ctx context.Context, size int64) ([]*videomodel.Video, error) {
	ids, _ := s.hotRecall(ctx, size) // hot + new
	return s.fetchAndScore(ctx, ids, size)
}

// ——— Helpers ———

func (s *RecommendService) getSeenIDs(ctx context.Context, userID string, limit int) []string {
	cursor, err := s.behaviorColl.Find(ctx, bson.M{
		"user_id": userID,
		"action":  bson.M{"$in": []string{"play", "like", "collect"}},
	})
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var logs []struct {
		TargetID  string    `bson:"target_id"`
		Timestamp time.Time `bson:"timestamp"`
	}
	cursor.All(ctx, &logs)
	sort.Slice(logs, func(i, j int) bool { return logs[i].Timestamp.After(logs[j].Timestamp) })

	var ids []string
	seen := make(map[string]bool)
	for _, l := range logs {
		if !seen[l.TargetID] {
			ids = append(ids, l.TargetID)
			seen[l.TargetID] = true
			if len(ids) >= limit {
				break
			}
		}
	}
	return ids
}

func (s *RecommendService) fetchAndScore(ctx context.Context, ids []string, limit int64) ([]*videomodel.Video, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var oids []primitive.ObjectID
	for _, id := range ids {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			oids = append(oids, oid)
		}
	}
	if len(oids) == 0 {
		return nil, nil
	}

	// 与 Feed 对齐可见性：仅已审核且媒体处理成功的视频进入推荐
	filter := bson.M{
		"_id":    bson.M{"$in": oids},
		"status": "approved",
		"$or": []bson.M{
			{"media_status": "succeeded"},
			{"media_status": bson.M{"$exists": false}},
		},
	}
	cursor, err := s.videoColl.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var videos []*videomodel.Video
	if err := cursor.All(ctx, &videos); err != nil {
		return nil, err
	}

	// 热度分（与 Feed 同一 hot_videos ZSet），统一评分公式
	now := time.Now()
	hotScores := make(map[string]float64, len(videos))
	var maxHot float64
	pipe := s.rdb.Client.Pipeline()
	cmds := make(map[string]*redis.FloatCmd, len(videos))
	for _, v := range videos {
		cmds[v.ID.Hex()] = pipe.ZScore(ctx, cache.HotVideosKey, v.ID.Hex())
	}
	maxCmd := pipe.ZRevRangeWithScores(ctx, cache.HotVideosKey, 0, 0)
	if _, err := pipe.Exec(ctx); err == nil {
		for id, cmd := range cmds {
			if s, err := cmd.Result(); err == nil {
				hotScores[id] = s
			}
		}
		if zs, err := maxCmd.Result(); err == nil && len(zs) > 0 {
			maxHot = zs[0].Score
		}
	}
	weights := videoService.DefaultFeedWeights()
	sort.Slice(videos, func(i, j int) bool {
		si := videoService.ScoreVideo(hotScores[videos[i].ID.Hex()], maxHot, videos[i].Tags, nil, videos[i].CreatedAt, weights, now)
		sj := videoService.ScoreVideo(hotScores[videos[j].ID.Hex()], maxHot, videos[j].Tags, nil, videos[j].CreatedAt, weights, now)
		return si > sj
	})

	if int64(len(videos)) > limit {
		videos = videos[:limit]
	}
	return videos, nil
}

func tagOverlap(a, b []string) int {
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	count := 0
	for _, t := range b {
		if set[t] {
			count++
		}
	}
	return count
}
