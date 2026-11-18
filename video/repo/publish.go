package repo

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"feedvideo.local/video/model"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrPublishConflict = errors.New("publish conflict")
var ErrPublishUnavailable = errors.New("publish unavailable")

type PublishStore interface {
	FindPublished(context.Context, string, string) (*model.Video, error)
	CreatePublished(context.Context, *model.Video) (*model.Video, bool, error)
	EnsurePublishIndexes(context.Context) error
}

var _ PublishStore = (*VideoRepoMongo)(nil)

func validPublishReceipt(video *model.Video) bool {
	receipt := video.PublishReceipt
	if receipt == nil || receipt.VideoID != video.ID.Hex() || receipt.MediaStatus == "" || receipt.ContentVersion < 0 {
		return false
	}
	if receipt.JobID != "" {
		job, err := uuid.Parse(receipt.JobID)
		if err != nil || job.Version() != 4 || job.String() != receipt.JobID || receipt.ContentVersion < 1 {
			return false
		}
	}
	return true
}

func (r *VideoRepoMongo) findPublished(ctx context.Context, owner, key string) (*model.Video, error) {
	if r == nil || r.mdb == nil || r.mdb.Col() == nil || ctx.Err() != nil {
		return nil, ErrPublishUnavailable
	}
	video, err := r.mdb.FindOne(ctx, bson.M{"author_id": owner, "publish_key": key})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, mongo.ErrNoDocuments
	}
	if err != nil {
		return nil, ErrPublishUnavailable
	}
	if video == nil || video.ID.IsZero() || video.AuthorID != owner || video.PublishKey != key || !validPublishHash(video.PublishHash) || (video.UploadID != "" && !validUploadUUID(video.UploadID)) || !validPublishReceipt(video) {
		return nil, ErrPublishUnavailable
	}
	return video, nil
}

// FindPublished always reads Mongo, including completed and deleted videos.
func (r *VideoRepoMongo) FindPublished(ctx context.Context, owner, key string) (*model.Video, error) {
	if !validUploadOwner(owner) || !validPublishKey(key) {
		return nil, ErrPublishConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.findPublished(ctx, owner, key)
}

func preparePublished(video *model.Video) (*model.Video, error) {
	if video == nil || video.ID.IsZero() || !validUploadOwner(video.AuthorID) || !validPublishKey(video.PublishKey) || !validPublishHash(video.PublishHash) || (video.UploadID != "" && !validUploadUUID(video.UploadID)) || video.ContentVersion < 0 || video.MediaStatus == "" {
		return nil, ErrPublishConflict
	}
	saved := *video
	if video.MediaJob != nil {
		job, err := uuid.Parse(video.MediaJob.JobID)
		if err != nil || job.Version() != 4 || job.String() != video.MediaJob.JobID || video.MediaJob.ContentVersion < 1 || video.MediaJob.ContentVersion != video.ContentVersion {
			return nil, ErrPublishConflict
		}
		jobCopy := *video.MediaJob
		saved.MediaJob = &jobCopy
	}
	jobID := ""
	if saved.MediaJob != nil {
		jobID = saved.MediaJob.JobID
	}
	want := model.PublishReceipt{VideoID: saved.ID.Hex(), MediaStatus: saved.MediaStatus, JobID: jobID, ContentVersion: saved.ContentVersion}
	if saved.PublishReceipt != nil && *saved.PublishReceipt != want {
		return nil, ErrPublishConflict
	}
	saved.PublishReceipt = &want
	if saved.Status == "" {
		saved.Status = "pending"
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	saved.CreatedAt, saved.UpdatedAt = now, now
	InitSearchProjection(&saved, now)
	return &saved, nil
}

// CreatePublished commits the supplied candidate ID, embedded job and first
// receipt together. Unknown results stop here; only duplicate keys read back.
func (r *VideoRepoMongo) CreatePublished(ctx context.Context, video *model.Video) (*model.Video, bool, error) {
	saved, err := preparePublished(video)
	if err != nil {
		return nil, false, err
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, false, ErrPublishUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticket, bootstrapErr := r.prepareOrdinaryStatsBootstrap(ctx, saved.ID.Hex())
	if bootstrapErr != nil {
		slog.Warn("new video statistics preparation unavailable", "video_id", saved.ID.Hex())
	}
	likeTicket := r.prepareVideoLikes(ctx, saved.ID.Hex())
	err = r.insertNewVideo(ctx, saved)
	if err == nil {
		if likeTicket != "" {
			if err := r.completeVideoLikes(ctx, saved.ID.Hex(), likeTicket); err != nil {
				slog.Warn("committed video likes initialization unavailable", "video_id", saved.ID.Hex())
			}
		}
		if ticket != "" {
			if err := r.completeOrdinaryStatsBootstrap(ctx, saved.ID.Hex(), ticket); err != nil {
				slog.Warn("new video statistics initialization unconfirmed", "video_id", saved.ID.Hex())
			}
		}
		if r.esClient != nil && !r.searchProjectionEnabled {
			snapshot := *saved
			snapshot.Tags = append([]string(nil), saved.Tags...)
			go r.spawnES("index", func() error { return r.indexToES(&snapshot) })
		}
		return saved, true, nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return nil, false, ErrPublishUnavailable
	}
	first, err := r.findPublished(ctx, saved.AuthorID, saved.PublishKey)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, false, ErrPublishConflict
	}
	if err != nil {
		return nil, false, err
	}
	if first.PublishHash != saved.PublishHash || first.UploadID != saved.UploadID {
		return nil, false, ErrPublishConflict
	}
	return first, false, nil
}

func (r *VideoRepoMongo) EnsurePublishIndexes(ctx context.Context) error {
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return ErrPublishUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.mdb.Col().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "author_id", Value: 1}, {Key: "publish_key", Value: 1}}, Options: options.Index().SetName("video_actor_publish_key_unique").SetUnique(true).SetPartialFilterExpression(bson.M{"publish_key": bson.M{"$type": "string", "$gt": ""}})},
		{Keys: bson.D{{Key: "upload_id", Value: 1}}, Options: options.Index().SetName("video_upload_id_unique").SetUnique(true).SetPartialFilterExpression(bson.M{"upload_id": bson.M{"$type": "string", "$gt": ""}})},
	})
	if err != nil {
		return ErrPublishUnavailable
	}
	return nil
}
