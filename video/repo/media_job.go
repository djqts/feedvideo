package repo

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"feedvideo.local/video/model"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MediaJobStore interface {
	Claim(context.Context, string, time.Time, time.Duration) (*model.MediaLease, error)
	Renew(context.Context, *model.MediaLease, time.Time, time.Duration) (bool, error)
	Complete(context.Context, *model.MediaLease, time.Time, *model.MediaResult) (bool, error)
	Fail(context.Context, *model.MediaLease, time.Time, string, bool) (bool, error)
	Get(context.Context, string) (*model.Video, error)
	RetryFailed(context.Context, string, string, string, int64, time.Time) (bool, error)
	EnsureIndexes(context.Context) error
}

type MediaJobRepo struct{ col *mongo.Collection }

func NewMediaJobRepo(db *mongo.Database) MediaJobStore {
	return &MediaJobRepo{col: db.Collection("videos")}
}

func mediaLiveFilter() bson.M {
	return bson.M{"status": bson.M{"$ne": "deleted"}, "deleted_at": bson.M{"$exists": false}}
}
func mediaLeaseFilter(lease *model.MediaLease, now time.Time) (bson.M, error) {
	if lease == nil || lease.VideoID.IsZero() || lease.JobID == "" || lease.ContentVersion < 1 || lease.LeaseOwner == "" || lease.LeaseToken < 1 {
		return nil, fmt.Errorf("invalid media lease")
	}
	filter := mediaLiveFilter()
	filter["_id"] = lease.VideoID
	filter["content_version"] = lease.ContentVersion
	filter["media_job.job_id"] = lease.JobID
	filter["media_job.content_version"] = lease.ContentVersion
	filter["media_job.status"] = "processing"
	filter["media_job.lease_owner"] = lease.LeaseOwner
	filter["media_job.lease_token"] = lease.LeaseToken
	filter["media_job.lease_until"] = bson.M{"$gt": now.UTC()}
	return filter, nil
}

func (r *MediaJobRepo) Claim(ctx context.Context, owner string, now time.Time, ttl time.Duration) (*model.MediaLease, error) {
	if owner == "" || ttl <= 0 {
		return nil, fmt.Errorf("invalid media lease owner or duration")
	}
	now = now.UTC()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	exhausted := mediaLiveFilter()
	exhausted["media_job.status"] = "processing"
	exhausted["media_job.attempts"] = bson.M{"$gte": 5}
	exhausted["media_job.lease_until"] = bson.M{"$lte": now}
	exhausted["$expr"] = bson.M{"$eq": bson.A{"$content_version", "$media_job.content_version"}}
	_, err := r.col.UpdateOne(ctx, SearchVersionFilter(exhausted), SearchSourcePipeline(bson.M{"media_job.status": "failed", "media_job.last_error_code": "attempts_exhausted", "media_job.updated_at": now, "media_status": "failed", "media_error": "attempts_exhausted", "transcode_status": "failed", "updated_at": now}, bson.M{"media_job.lease_owner": "", "media_job.lease_until": ""}, now))
	if err != nil {
		return nil, fmt.Errorf("terminalize expired media job: %w", err)
	}
	filter := mediaLiveFilter()
	filter["media_job.attempts"] = bson.M{"$lt": 5}
	filter["$expr"] = bson.M{"$eq": bson.A{"$content_version", "$media_job.content_version"}}
	filter["$or"] = bson.A{bson.M{"media_job.status": bson.M{"$in": bson.A{"queued", "retry_wait"}}, "media_job.next_attempt_at": bson.M{"$lte": now}}, bson.M{"media_job.status": "processing", "media_job.lease_until": bson.M{"$lte": now}}}
	update := searchSourcePipeline(bson.M{"media_job.status": "processing", "media_job.lease_owner": owner, "media_job.lease_until": now.Add(ttl), "media_job.next_attempt_at": now.Add(ttl), "media_job.updated_at": now, "media_status": "processing", "transcode_status": "processing", "updated_at": now}, nil, bson.M{"media_job.lease_token": bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$media_job.lease_token", int64(0)}}, int64(1)}}, "media_job.attempts": bson.M{"$add": bson.A{"$media_job.attempts", int64(1)}}}, now)
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After).SetSort(bson.D{{Key: "media_job.next_attempt_at", Value: 1}, {Key: "media_job.lease_until", Value: 1}, {Key: "_id", Value: 1}})
	var video model.Video
	err = r.col.FindOneAndUpdate(ctx, SearchVersionFilter(filter), update, opts).Decode(&video)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim media job: %w", err)
	}
	if video.MediaJob == nil {
		return nil, fmt.Errorf("claimed video has no media job")
	}
	job := video.MediaJob
	jobID, idErr := uuid.Parse(job.JobID)
	if video.ID.IsZero() || video.Status == "deleted" || video.DeletedAt != nil || video.ContentVersion != job.ContentVersion || job.ContentVersion < 1 || idErr != nil || jobID.Version() != 4 || jobID.String() != job.JobID || job.Status != "processing" || job.LeaseOwner != owner || job.LeaseToken < 1 || job.Attempts < 1 || job.Attempts > 5 || !job.LeaseUntil.After(now) || !job.LeaseUntil.Equal(now.Add(ttl).Truncate(time.Millisecond)) || job.SourceBucket == "" || job.SourceObject == "" {
		return nil, fmt.Errorf("invalid claimed media lease")
	}
	return &model.MediaLease{VideoID: video.ID, JobID: job.JobID, ContentVersion: job.ContentVersion, LeaseOwner: job.LeaseOwner, LeaseToken: job.LeaseToken, LeaseUntil: job.LeaseUntil, Attempt: job.Attempts, SourceBucket: job.SourceBucket, SourceObject: job.SourceObject, CoverURL: video.CoverURL}, nil
}

func (r *MediaJobRepo) updateLease(ctx context.Context, lease *model.MediaLease, now time.Time, update bson.M, extra ...bson.M) (bool, error) {
	filter, err := mediaLeaseFilter(lease, now)
	if err != nil {
		return false, err
	}
	for _, fields := range extra {
		for key, value := range fields {
			filter[key] = value
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var write any = update
	set, _ := update["$set"].(bson.M)
	for field := range set {
		if searchSourceFields[field] {
			unset, _ := update["$unset"].(bson.M)
			write = SearchSourcePipeline(set, unset, now)
			filter = SearchVersionFilter(filter)
			break
		}
	}
	result, err := r.col.UpdateOne(ctx, filter, write)
	if err != nil {
		return false, err
	}
	return result.MatchedCount == 1, nil
}

func (r *MediaJobRepo) Renew(ctx context.Context, lease *model.MediaLease, now time.Time, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("invalid media lease duration")
	}
	now = now.UTC()
	return r.updateLease(ctx, lease, now, bson.M{"$set": bson.M{"media_job.lease_until": now.Add(ttl), "media_job.next_attempt_at": now.Add(ttl), "media_job.updated_at": now, "updated_at": now}})
}

func (r *MediaJobRepo) Complete(ctx context.Context, lease *model.MediaLease, now time.Time, result *model.MediaResult) (bool, error) {
	if err := model.ValidateMediaResult(lease, result); err != nil {
		return false, err
	}
	now = now.UTC()
	set := bson.M{"media_job.status": "succeeded", "media_job.output_manifest": result.Manifest, "media_job.last_error_code": "", "media_job.updated_at": now, "media_status": "succeeded", "media_error": "", "transcode_status": "ready", "duration": result.Duration, "width": result.Width, "height": result.Height, "updated_at": now, "retry_count": max(0, lease.Attempt-1)}
	extra := bson.M{}
	for _, a := range result.Manifest {
		url := "/storage/" + a.Bucket + "/" + a.ObjectKey
		switch a.Kind {
		case "480p":
			set["transcoded_url"] = url
		case "720p":
			set["transcoded_url_720p"] = url
		case "cover":
			set["cover_url"] = url
			extra["cover_url"] = lease.CoverURL
		}
	}
	return r.updateLease(ctx, lease, now, bson.M{"$set": set, "$unset": bson.M{"media_job.lease_owner": "", "media_job.lease_until": ""}}, extra)
}

func safeMediaError(code string) string {
	switch code {
	case "source_download_failed", "invalid_media", "transcode_failed", "cover_failed", "upload_failed", "timeout", "lease_lost", "attempts_exhausted", "internal_error":
		return code
	}
	return "internal_error"
}
func (r *MediaJobRepo) Fail(ctx context.Context, lease *model.MediaLease, now time.Time, code string, retryable bool) (bool, error) {
	if lease == nil {
		return false, fmt.Errorf("invalid media lease")
	}
	now = now.UTC()
	code = safeMediaError(code)
	set := bson.M{"media_job.status": "failed", "media_job.last_error_code": code, "media_job.updated_at": now, "media_status": "failed", "media_error": code, "transcode_status": "failed", "updated_at": now, "retry_count": max(0, lease.Attempt-1)}
	if retryable && lease.Attempt >= 1 && lease.Attempt < 5 {
		delays := []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, 3 * time.Minute}
		base := delays[lease.Attempt-1]
		set["media_job.status"] = "retry_wait"
		set["media_status"] = "retry_wait"
		set["media_job.next_attempt_at"] = now.Add(base + time.Duration(rand.Int64N(int64(base/5)+1)))
	}
	return r.updateLease(ctx, lease, now, bson.M{"$set": set, "$unset": bson.M{"media_job.lease_owner": "", "media_job.lease_until": ""}})
}

func (r *MediaJobRepo) Get(ctx context.Context, id string) (*model.Video, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filter := mediaLiveFilter()
	filter["_id"] = oid
	var video model.Video
	err = r.col.FindOne(ctx, filter).Decode(&video)
	if err != nil {
		return nil, err
	}
	return &video, nil
}

func (r *MediaJobRepo) RetryFailed(ctx context.Context, id, author, job string, version int64, now time.Time) (bool, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return false, err
	}
	if author == "" || job == "" || version < 1 {
		return false, fmt.Errorf("invalid media retry identity")
	}
	now = now.UTC()
	filter := mediaLiveFilter()
	filter["_id"] = oid
	filter["author_id"] = author
	filter["content_version"] = version
	filter["media_job.job_id"] = job
	filter["media_job.content_version"] = version
	filter["media_job.status"] = "failed"
	update := SearchSourcePipeline(bson.M{"media_job.status": "queued", "media_job.attempts": 0, "media_job.next_attempt_at": now, "media_job.last_error_code": "", "media_job.updated_at": now, "media_status": "queued", "media_error": "", "transcode_status": "pending", "updated_at": now}, bson.M{"media_job.lease_owner": "", "media_job.lease_until": ""}, now)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := r.col.UpdateOne(ctx, SearchVersionFilter(filter), update)
	if err != nil {
		return false, err
	}
	return result.MatchedCount == 1, nil
}

func (r *MediaJobRepo) EnsureIndexes(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, field := range []string{"media_job.next_attempt_at", "media_job.lease_until"} {
		index := mongo.IndexModel{Keys: bson.D{{Key: "media_job.status", Value: 1}, {Key: field, Value: 1}, {Key: "_id", Value: 1}}}
		if _, err := r.col.Indexes().CreateOne(ctx, index); err != nil {
			return fmt.Errorf("ensure media job index %s: %w", field, err)
		}
	}
	return nil
}
