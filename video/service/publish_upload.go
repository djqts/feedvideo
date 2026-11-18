package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// UploadObjectValidator verifies the object still matches the complete upload receipt.
type UploadObjectValidator interface {
	ValidateUploadObject(context.Context, string, string, string, string, int64) error
}

func (s *VideoService) ConfigureUploads(uploads repo.UploadStore, published repo.PublishStore, videoBucket, coverBucket string, validator MediaSourceValidator) {
	s.uploadStore, s.publishStore = uploads, published
	s.videoBucket, s.coverBucket, s.sourceValidator = videoBucket, coverBucket, validator
	s.uploadsConfigured = true
}

// ValidPublishActor rejects alternate decimal spellings of the server authenticated identity.
func ValidPublishActor(actor string) bool {
	id, err := strconv.ParseInt(actor, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == actor
}

func ValidPublishKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", rune(c))) {
			return false
		}
	}
	return true
}

func canonicalUploadID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func validateConfiguredPublish(req *PublishReq) error {
	if !ValidPublishActor(req.AuthorID) || req.Title == "" || req.UploadID == "" && req.VideoURL == "" {
		return ErrBadRequest
	}
	if req.PublishKey != "" && !ValidPublishKey(req.PublishKey) || req.UploadID != "" && (!canonicalUploadID(req.UploadID) || !ValidPublishKey(req.PublishKey)) {
		return ErrBadRequest
	}
	if utf8.RuneCountInString(req.Title) > 100 || utf8.RuneCountInString(req.Description) > 2000 || len(req.Tags) > 10 {
		return ErrBadRequest
	}
	for _, tag := range req.Tags {
		if utf8.RuneCountInString(tag) > 30 {
			return ErrBadRequest
		}
	}
	if math.IsNaN(req.Longitude) || math.IsInf(req.Longitude, 0) || math.IsNaN(req.Latitude) || math.IsInf(req.Latitude, 0) || req.Longitude < -180 || req.Longitude > 180 || req.Latitude < -90 || req.Latitude > 90 {
		return ErrBadRequest
	}
	return nil
}

func publishPayloadHash(req *PublishReq) (string, error) {
	// Fixed fields and normalized empty tags keep the intent stable across JSON clients.
	payload := struct {
		Title, Description                  string
		Tags                                []string
		Music, UploadID, VideoURL, CoverURL string
		Longitude, Latitude                 float64
	}{Title: req.Title, Description: req.Description, Tags: req.Tags, Music: req.Music, UploadID: req.UploadID, Longitude: req.Longitude, Latitude: req.Latitude}
	if len(payload.Tags) == 0 {
		payload.Tags = []string{}
	}
	if req.UploadID == "" {
		payload.VideoURL, payload.CoverURL = req.VideoURL, req.CoverURL
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", ErrBadRequest
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (s *VideoService) publishConfigured(ctx context.Context, req *PublishReq) (*PublishResp, error) {
	if err := validateConfiguredPublish(req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.publishStore == nil {
		return nil, ErrMediaUnavailable
	}
	hash, err := publishPayloadHash(req)
	if err != nil {
		return nil, err
	}
	if req.PublishKey != "" {
		old, lookupErr := s.publishStore.FindPublished(ctx, req.AuthorID, req.PublishKey)
		if lookupErr == nil {
			if old == nil || old.PublishHash != hash || old.AuthorID != req.AuthorID || old.PublishKey != req.PublishKey {
				if old == nil {
					return nil, ErrMediaUnavailable
				}
				return nil, repo.ErrPublishConflict
			}
			return s.publishedAcceptance(ctx, old, true)
		}
		if !errors.Is(lookupErr, mongo.ErrNoDocuments) {
			return nil, ErrMediaUnavailable
		}
	}
	if s.sourceValidator == nil || req.UploadID != "" && s.uploadStore == nil {
		return nil, ErrMediaUnavailable
	}
	// A replay uses its immutable accepted payload even if current content policy has changed.
	if s.sensitive != nil {
		hit, checkErr := s.sensitive.ContainsSensitive(ctx, req.Title+" "+req.Description)
		if checkErr == nil && hit {
			return nil, ErrBadRequest
		}
	}
	video := &model.Video{ID: primitive.NewObjectID(), Title: req.Title, Description: req.Description, Tags: req.Tags, Music: req.Music, AuthorID: req.AuthorID, Status: "pending", MediaStatus: "queued", TranscodeStatus: "pending", VideoURL: req.VideoURL, CoverURL: req.CoverURL}
	var source model.UploadObject
	jobID := uuid.NewString()
	if req.UploadID != "" {
		u, getErr := s.uploadStore.Get(ctx, req.AuthorID, req.UploadID)
		if getErr != nil {
			return nil, uploadPublishError(getErr)
		}
		if u == nil || u.OwnerID != req.AuthorID || u.ID != req.UploadID {
			return nil, repo.ErrUploadNotFound
		}
		if u.Status == "reserved" || u.Status == "claimed" {
			if u.PublishKey != req.PublishKey || u.PayloadHash != hash {
				return nil, repo.ErrPublishConflict
			}
		} else if u.Status != "ready" {
			return nil, repo.ErrUploadConflict
		} else if now := time.Now().UTC(); !now.Before(u.LeaseExpiresAt) || !now.Before(u.MaximumExpiresAt) {
			return nil, repo.ErrUploadExpired
		}
		objects, objectErr := s.resolveUploadObjects(ctx, u)
		if objectErr != nil {
			return nil, objectErr
		}
		reserved, reserveErr := s.uploadStore.Reserve(ctx, req.AuthorID, req.UploadID, req.PublishKey, hash, time.Now().UTC())
		if reserveErr != nil {
			return nil, uploadPublishError(reserveErr)
		}
		if reserved == nil || reserved.OwnerID != req.AuthorID || reserved.ID != req.UploadID || reserved.PublishKey != req.PublishKey || reserved.PayloadHash != hash {
			return nil, ErrMediaUnavailable
		}
		candidate, idErr := primitive.ObjectIDFromHex(reserved.VideoID)
		if idErr != nil || candidate == primitive.NilObjectID || !canonicalUploadID(reserved.JobID) {
			return nil, ErrMediaUnavailable
		}
		video.ID, jobID, video.UploadID, video.ContentVersion = candidate, reserved.JobID, req.UploadID, u.ContentVersion
		source = objects["video"]
		video.VideoURL = "/storage/" + source.Bucket + "/" + source.ObjectKey
		video.CoverURL = ""
		if cover, exists := objects["cover"]; exists {
			video.CoverURL = "/storage/" + cover.Bucket + "/" + cover.ObjectKey
		}
		// The handler's legacy queue must receive the registry source, including when media jobs are disabled.
		req.VideoURL, req.CoverURL = video.VideoURL, video.CoverURL
	} else {
		object, sourceErr := s.validateLegacyPublishSource(ctx, req.VideoURL, s.videoBucket, req.AuthorID)
		if sourceErr != nil {
			return nil, sourceErr
		}
		source = model.UploadObject{Bucket: s.videoBucket, ObjectKey: object}
		if req.CoverURL != "" {
			if _, sourceErr := s.validateLegacyPublishSource(ctx, req.CoverURL, s.coverBucket, req.AuthorID); sourceErr != nil {
				return nil, sourceErr
			}
		}
	}
	if req.Longitude != 0 || req.Latitude != 0 {
		video.Location = &model.Geo{Type: "Point", Coordinates: []float64{req.Longitude, req.Latitude}}
	}
	now := time.Now().UTC()
	if s.mediaJobsEnabled {
		if video.ContentVersion == 0 {
			video.ContentVersion = 1
		}
		video.MediaJob = &model.MediaJob{JobID: jobID, ContentVersion: video.ContentVersion, Status: "queued", SourceKind: "object", SourceBucket: source.Bucket, SourceObject: source.ObjectKey, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	}
	video.PublishKey, video.PublishHash = req.PublishKey, hash
	video.PublishReceipt = &model.PublishReceipt{VideoID: video.ID.Hex(), MediaStatus: video.MediaStatus, ContentVersion: video.ContentVersion}
	if video.MediaJob != nil {
		video.PublishReceipt.MediaStatus, video.PublishReceipt.JobID = video.MediaJob.Status, video.MediaJob.JobID
		video.PublishReceipt.ContentVersion = video.ContentVersion
	}
	inserted := true
	if req.PublishKey != "" {
		var writeErr error
		video, inserted, writeErr = s.publishStore.CreatePublished(ctx, video)
		if writeErr != nil {
			if errors.Is(writeErr, repo.ErrPublishConflict) {
				return nil, repo.ErrPublishConflict
			}
			return nil, ErrMediaUnavailable
		}
	} else {
		// The compatibility repository assigns an ID during Create; derive this response from that committed ID.
		video.PublishHash, video.PublishReceipt = "", nil
		if err := s.videoRepo.Create(ctx, video); err != nil {
			return nil, ErrMediaUnavailable
		}
		video.PublishReceipt = &model.PublishReceipt{VideoID: video.ID.Hex()}
		if video.MediaJob != nil {
			video.PublishReceipt.MediaStatus, video.PublishReceipt.JobID = video.MediaJob.Status, video.MediaJob.JobID
			video.PublishReceipt.ContentVersion = video.ContentVersion
		}
	}
	if video == nil || video.ID == primitive.NilObjectID {
		return nil, ErrMediaUnavailable
	}
	resp, err := s.publishedAcceptance(ctx, video, !inserted)
	if err != nil {
		return nil, err
	}
	if inserted {
		s.notifyPublishedTopics(video.Tags)
	}
	return resp, nil
}

func uploadPublishError(err error) error {
	for _, known := range []error{repo.ErrUploadNotFound, repo.ErrUploadExpired, repo.ErrUploadConflict} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, repo.ErrUploadInvalid) {
		return ErrBadRequest
	}
	return ErrMediaUnavailable
}

func (s *VideoService) resolveUploadObjects(ctx context.Context, u *model.Upload) (map[string]model.UploadObject, error) {
	validator, ok := s.sourceValidator.(UploadObjectValidator)
	if !ok {
		return nil, ErrMediaUnavailable
	}
	if u.ContentVersion != 1 || len(u.Objects) == 0 || len(u.Objects) > 2 {
		return nil, ErrBadRequest
	}
	objects := make(map[string]model.UploadObject, len(u.Objects))
	for _, obj := range u.Objects {
		bucket := s.videoBucket
		if obj.Kind == "cover" {
			bucket = s.coverBucket
		} else if obj.Kind != "video" {
			return nil, ErrBadRequest
		}
		prefix := u.OwnerID + "/uploads/" + u.ID + "/" + obj.Kind + "."
		suffix := strings.TrimPrefix(obj.ObjectKey, prefix)
		if bucket == "" || obj.Bucket != bucket || !strings.HasPrefix(obj.ObjectKey, prefix) || len(suffix) == 0 || len(suffix) > 16 || obj.Size <= 0 || obj.ContentType == "" || len(obj.SHA256) != 64 {
			return nil, ErrBadRequest
		}
		for _, c := range suffix {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return nil, ErrBadRequest
			}
		}
		decoded, err := hex.DecodeString(obj.SHA256)
		if err != nil || len(decoded) != 32 || strings.ToLower(obj.SHA256) != obj.SHA256 {
			return nil, ErrBadRequest
		}
		if _, duplicate := objects[obj.Kind]; duplicate {
			return nil, ErrBadRequest
		}
		objects[obj.Kind] = obj
	}
	if _, ok := objects["video"]; !ok {
		return nil, ErrBadRequest
	}
	// Validate every provided object only after the entire registry reference has passed structural checks.
	for _, obj := range u.Objects {
		if err := validator.ValidateUploadObject(ctx, obj.Bucket, obj.ObjectKey, obj.ObjectVersion, obj.SHA256, obj.Size); err != nil {
			return nil, ErrMediaUnavailable
		}
	}
	return objects, nil
}

func (s *VideoService) validateLegacyPublishSource(ctx context.Context, raw, bucket, author string) (string, error) {
	object, err := ValidateMediaSourceReference(raw, bucket, author)
	if err != nil {
		return "", ErrBadRequest
	}
	if strings.HasPrefix(object, author+"/uploads/") {
		return "", ErrBadRequest
	}
	if err := s.sourceValidator.ValidateMediaSource(ctx, bucket, object); err != nil {
		return "", ErrMediaUnavailable
	}
	return object, nil
}

func (s *VideoService) publishedAcceptance(ctx context.Context, video *model.Video, replay bool) (*PublishResp, error) {
	receipt := video.PublishReceipt
	if receipt == nil || receipt.VideoID != video.ID.Hex() {
		return nil, ErrMediaUnavailable
	}
	if video.UploadID != "" {
		if s.uploadStore == nil {
			slog.Warn("published upload claim remains recoverable", "upload_id", video.UploadID, "video_id", video.ID.Hex())
		} else if err := s.uploadStore.MarkClaimed(ctx, video.AuthorID, video.UploadID, video.PublishKey, video.PublishHash, video.ID.Hex()); err != nil {
			slog.Warn("published upload claim remains recoverable", "upload_id", video.UploadID, "video_id", video.ID.Hex())
		}
	}
	return &PublishResp{VideoID: receipt.VideoID, MediaStatus: receipt.MediaStatus, JobID: receipt.JobID, ContentVersion: receipt.ContentVersion, Replayed: replay}, nil
}

func (s *VideoService) notifyPublishedTopics(tags []string) {
	if s.topicNotifier == nil || len(tags) == 0 {
		return
	}
	copyTags := append([]string(nil), tags...)
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, tag := range copyTags {
			if err := s.topicNotifier.IncrHot(ctx, tag); err != nil {
				slog.Warn("incr topic hot failed", "tag", tag)
			}
		}
	}()
}
