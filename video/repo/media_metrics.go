package repo

import (
	"context"
	"fmt"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MediaJobMetricsReader is deliberately separate from the job write boundary.
type MediaJobMetricsReader interface {
	Snapshot(context.Context, time.Time) (model.MediaJobMetrics, error)
}

func NewMediaJobMetricsReader(db *mongo.Database) MediaJobMetricsReader {
	return &MediaJobRepo{col: db.Collection("videos")}
}

func (r *MediaJobRepo) Snapshot(parent context.Context, now time.Time) (model.MediaJobMetrics, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	filter := mediaLiveFilter()
	filter["media_job.status"] = bson.M{"$in": bson.A{"queued", "retry_wait", "processing", "succeeded", "failed"}}
	active := bson.M{"$and": bson.A{
		bson.M{"$eq": bson.A{"$media_job.status", "processing"}},
		bson.M{"$gt": bson.A{"$media_job.lease_until", now.UTC()}},
	}}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$group", Value: bson.M{
			"_id": "$media_job.status", "count": bson.M{"$sum": 1},
			"active": bson.M{"$sum": bson.M{"$cond": bson.A{active, 1, 0}}},
			"oldest": bson.M{"$min": "$media_job.created_at"},
		}}},
	}
	cursor, err := r.col.Aggregate(ctx, pipeline, options.Aggregate().SetMaxTime(5*time.Second).SetBatchSize(5))
	if err != nil {
		return model.MediaJobMetrics{}, fmt.Errorf("media metrics aggregate: %w", err)
	}
	defer cursor.Close(ctx)
	var snapshot model.MediaJobMetrics
	for cursor.Next(ctx) {
		var row struct {
			Status string    `bson:"_id"`
			Count  int64     `bson:"count"`
			Active int64     `bson:"active"`
			Oldest time.Time `bson:"oldest"`
		}
		if err := cursor.Decode(&row); err != nil {
			return model.MediaJobMetrics{}, fmt.Errorf("media metrics decode: %w", err)
		}
		switch row.Status {
		case "queued":
			snapshot.Queued = row.Count
		case "retry_wait":
			snapshot.RetryWait = row.Count
		case "processing":
			snapshot.Processing, snapshot.Active = row.Count, row.Active
		case "succeeded":
			snapshot.Succeeded = row.Count
		case "failed":
			snapshot.Failed = row.Count
		default:
			return model.MediaJobMetrics{}, fmt.Errorf("unknown media metrics state")
		}
		if (row.Status == "queued" || row.Status == "retry_wait" || row.Status == "processing") && !row.Oldest.IsZero() &&
			(snapshot.OldestCreatedAt.IsZero() || row.Oldest.Before(snapshot.OldestCreatedAt)) {
			snapshot.OldestCreatedAt = row.Oldest
		}
	}
	if err := cursor.Err(); err != nil {
		return model.MediaJobMetrics{}, fmt.Errorf("media metrics cursor: %w", err)
	}
	return snapshot, nil
}
