package repo

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"feedvideo.local/user/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// behaviorJob 待写入的行为日志项。
type behaviorJob struct {
	userID     int64
	action     string
	targetID   string
	targetType string
}

type UserBehavior struct {
	col     *mongo.Collection
	ch      chan behaviorJob
	once    sync.Once
	dropped atomic.Int64
	workerWG sync.WaitGroup
}

func NewUserBehaviorRepo(col *mongo.Collection) *UserBehavior {
	// Ensure index
	_, err := col.Indexes().CreateOne(context.Background(),
		mongo.IndexModel{
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "timestamp", Value: -1}},
			Options: options.Index().SetBackground(true),
		},
	)
	if err != nil {
		slog.Warn("create behavior index", "error", err)
	}
	r := &UserBehavior{
		col: col,
		ch:  make(chan behaviorJob, 1024),
	}
	// 有界 worker 池：固定 2 个消费协程，避免每事件一个 goroutine
	r.workerWG.Add(2)
	for i := 0; i < 2; i++ {
		go r.worker()
	}
	return r
}

// LogAsync 异步记录用户行为日志（有界队列，满则丢弃并计数，2s 写超时）。
func LogAsync(repo *UserBehavior, userID int64, action, targetID, targetType string) {
	if repo == nil || userID == 0 {
		return
	}
	select {
	case repo.ch <- behaviorJob{userID: userID, action: action, targetID: targetID, targetType: targetType}:
	default:
		repo.dropped.Add(1)
		slog.Warn("behavior queue full, drop log", "action", action)
	}
}

// worker 消费行为日志队列，单条 2s 超时写入 Mongo。
func (r *UserBehavior) worker() {
	defer r.workerWG.Done()
	for job := range r.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		log := &model.UserBehaviorLog{
			UserID:     job.userID,
			Action:     job.action,
			TargetID:   job.targetID,
			TargetType: job.targetType,
			Timestamp:  time.Now(),
		}
		if err := r.InsertLog(ctx, log); err != nil {
			slog.Warn("behavior log failed", "action", job.action, "error", err)
		}
		cancel()
	}
}

// Close 幂等关闭日志队列（worker 排空后退出），供进程优雅退出调用。
func (r *UserBehavior) Close() {
	r.once.Do(func() { close(r.ch) })
}

// Wait 等待 worker 排空队列后退出，超时返回 false（进程退出兜底）。
func (r *UserBehavior) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		r.workerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
func (r *UserBehavior) InsertLog(ctx context.Context, log *model.UserBehaviorLog) error {
	_, err := r.col.InsertOne(ctx, log)
	return err
}

func (r *UserBehavior) QueryLog(ctx context.Context, userID int64, from, to time.Time) ([]*model.UserBehaviorLog, error) {
	filter := bson.M{
		"user_id":   userID,
		"timestamp": bson.M{"$gte": from, "$lte": to},
	}
	cursor, err := r.col.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var logs []*model.UserBehaviorLog
	if err := cursor.All(ctx, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}

// AggregateStats 聚合指定天数内的行为统计。
func (r *UserBehavior) AggregateStats(ctx context.Context, userID int64, days int) (map[string]int64, error) {
	from := time.Now().AddDate(0, 0, -days)

	match := bson.M{
		"timestamp":   bson.M{"$gte": from},
		"action":      bson.M{"$exists": true, "$ne": ""},
		"target_type": bson.M{"$exists": true, "$ne": ""},
	}
	// userID == 0 → 平台范围聚合（管理端看板）
	if userID != 0 {
		match["user_id"] = userID
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id":   "$action",
			"count": bson.M{"$sum": 1},
		}}},
	}

	cursor, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		Action string `bson:"_id"`
		Count  int64  `bson:"count"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	// Merge into result map
	stats := make(map[string]int64, len(results))
	for _, r := range results {
		stats[r.Action] = r.Count
	}
	return stats, nil
}
