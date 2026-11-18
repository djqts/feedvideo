package repo

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"feedvideo.local/video/model"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	ErrUploadInvalid     = errors.New("invalid upload")
	ErrUploadNotFound    = errors.New("upload not found")
	ErrUploadExpired     = errors.New("upload expired")
	ErrUploadConflict    = errors.New("upload conflict")
	ErrUploadUnavailable = errors.New("upload unavailable")
)

// String arguments are ordered owner, id, key, hash, videoID throughout.
type UploadStore interface {
	Create(context.Context, *model.Upload) error
	Complete(context.Context, string, string, []model.UploadObject) (*model.Upload, error)
	Fail(context.Context, string, string) error
	Get(context.Context, string, string) (*model.Upload, error)
	Renew(context.Context, string, string, time.Time) (*model.Upload, error)
	Reserve(context.Context, string, string, string, string, time.Time) (*model.Upload, error)
	MarkClaimed(context.Context, string, string, string, string, string) error
	EnsureIndexes(context.Context) error
}

type uploadRepo struct{ col *mongo.Collection }

func NewUploadRepo(db *mongo.Database) UploadStore {
	r := &uploadRepo{}
	if db != nil {
		r.col = db.Collection("uploads")
	}
	return r
}

func validUploadOwner(owner string) bool {
	n, err := strconv.ParseInt(owner, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == owner
}

func validUploadUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func validPublishKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

func validPublishHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == hash
}

func validUploadVideoID(id string) bool {
	oid, err := primitive.ObjectIDFromHex(id)
	return err == nil && !oid.IsZero() && oid.Hex() == id
}

func validateUploadIdentity(owner, id string) error {
	if !validUploadOwner(owner) || !validUploadUUID(id) {
		return ErrUploadInvalid
	}
	return nil
}

func validateUploadObjects(owner, id string, objects []model.UploadObject, complete bool) error {
	if len(objects) < 1 || len(objects) > 2 {
		return ErrUploadInvalid
	}
	prefix := owner + "/uploads/" + id + "/"
	seen := map[string]bool{}
	for _, o := range objects {
		if (o.Kind != "video" && o.Kind != "cover") || seen[o.Kind] {
			return ErrUploadInvalid
		}
		seen[o.Kind] = true
		if o.Bucket == "" || o.Bucket == "." || o.Bucket == ".." || strings.ContainsAny(o.Bucket, "/\\%?# ") || strings.ContainsAny(o.ObjectKey, "\\%?#") || strings.IndexFunc(o.Bucket+o.ObjectKey+o.ContentType+o.ObjectVersion, unicode.IsControl) >= 0 || !strings.HasPrefix(o.ObjectKey, prefix) || o.ObjectKey == prefix || strings.HasSuffix(o.ObjectKey, "/") || path.Clean(o.ObjectKey) != o.ObjectKey {
			return ErrUploadInvalid
		}
		if o.Size < 0 || (o.SHA256 != "" && !validPublishHash(o.SHA256)) {
			return ErrUploadInvalid
		}
		if complete && (o.Size <= 0 || strings.TrimSpace(o.ContentType) == "" || !validPublishHash(o.SHA256)) {
			return ErrUploadInvalid
		}
	}
	if !seen["video"] {
		return ErrUploadInvalid
	}
	return nil
}

func uploadObjectsEqual(a, b []model.UploadObject, metadata bool) bool {
	if len(a) != len(b) {
		return false
	}
	byKind := make(map[string]model.UploadObject, len(a))
	for _, o := range a {
		byKind[o.Kind] = o
	}
	for _, o := range b {
		registered, ok := byKind[o.Kind]
		if !ok || registered.Bucket != o.Bucket || registered.ObjectKey != o.ObjectKey || (metadata && registered != o) {
			return false
		}
	}
	return true
}

func uploadHasReservation(u *model.Upload) bool {
	return u.PublishKey != "" || u.PayloadHash != "" || u.VideoID != "" || u.JobID != ""
}

func validUploadRecord(u *model.Upload) bool {
	if validateUploadIdentity(u.OwnerID, u.ID) != nil || u.Version < 1 || u.ContentVersion != 1 || u.CreatedAt.IsZero() || u.UpdatedAt.Before(u.CreatedAt) || u.LeaseExpiresAt.Before(u.CreatedAt) || u.LeaseExpiresAt.After(u.MaximumExpiresAt) || !u.MaximumExpiresAt.After(u.CreatedAt) || u.MaximumExpiresAt.After(u.CreatedAt.Add(7*24*time.Hour)) {
		return false
	}
	complete := false
	switch u.Status {
	case "uploading", "failed":
	case "ready", "reserved", "claimed":
		complete = true
	default:
		return false
	}
	if validateUploadObjects(u.OwnerID, u.ID, u.Objects, complete) != nil {
		return false
	}
	if u.Status == "reserved" || u.Status == "claimed" {
		job, err := uuid.Parse(u.JobID)
		return validPublishKey(u.PublishKey) && validPublishHash(u.PayloadHash) && validUploadVideoID(u.VideoID) && err == nil && job.Version() == 4 && job.String() == u.JobID
	}
	return !uploadHasReservation(u)
}

func (r *uploadRepo) Create(ctx context.Context, upload *model.Upload) error {
	if upload == nil || validateUploadIdentity(upload.OwnerID, upload.ID) != nil || upload.Status != "uploading" || upload.Version != 1 || upload.ContentVersion != 1 || uploadHasReservation(upload) || validateUploadObjects(upload.OwnerID, upload.ID, upload.Objects, false) != nil {
		return ErrUploadInvalid
	}
	if r == nil || r.col == nil {
		return ErrUploadUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	saved := *upload
	saved.Objects = append([]model.UploadObject(nil), upload.Objects...)
	saved.CreatedAt, saved.UpdatedAt = now, now
	saved.LeaseExpiresAt, saved.MaximumExpiresAt = now.Add(24*time.Hour), now.Add(7*24*time.Hour)
	_, err := r.col.InsertOne(ctx, &saved)
	if mongo.IsDuplicateKeyError(err) {
		return ErrUploadConflict
	}
	if err != nil {
		return ErrUploadUnavailable
	}
	*upload = saved
	return nil
}

// read uses the caller's existing budget. Missing and foreign-owned records have
// the same result; dependency/decode failures are never absence.
func (r *uploadRepo) read(ctx context.Context, owner, id string) (*model.Upload, error) {
	if r == nil || r.col == nil || ctx.Err() != nil {
		return nil, ErrUploadUnavailable
	}
	var upload model.Upload
	err := r.col.FindOne(ctx, bson.M{"_id": id, "owner_id": owner}).Decode(&upload)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrUploadNotFound
	}
	if err != nil {
		return nil, ErrUploadUnavailable
	}
	if upload.ID != id || upload.OwnerID != owner {
		return nil, ErrUploadNotFound
	}
	if !validUploadRecord(&upload) {
		return nil, ErrUploadUnavailable
	}
	return &upload, nil
}

func (r *uploadRepo) Get(ctx context.Context, owner, id string) (*model.Upload, error) {
	if err := validateUploadIdentity(owner, id); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.read(ctx, owner, id)
}

type uploadChange func(*model.Upload) (*model.Upload, bson.M, bson.M, error)

// mutate performs at most eight state/version CAS writes under one five-second
// budget. Only a known CAS miss rereads; unknown writes stop immediately.
func (r *uploadRepo) mutate(ctx context.Context, owner, id string, change uploadChange) (*model.Upload, error) {
	for attempt := 0; attempt < 8; attempt++ {
		current, err := r.read(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		next, set, extra, err := change(current)
		if err != nil {
			return nil, err
		}
		if set == nil {
			return current, nil
		}
		if current.Version == math.MaxInt64 {
			return nil, ErrUploadInvalid
		}
		query := bson.M{"_id": id, "owner_id": owner, "status": current.Status, "version": current.Version}
		for k, v := range extra {
			query[k] = v
		}
		next.Version = current.Version + 1
		next.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		set["updated_at"] = next.UpdatedAt
		result, err := r.col.UpdateOne(ctx, query, bson.M{"$set": set, "$inc": bson.M{"version": 1}})
		if err != nil {
			return nil, ErrUploadUnavailable
		}
		if result.MatchedCount == 1 {
			return next, nil
		}
	}
	return nil, ErrUploadConflict
}

func cloneUpload(u *model.Upload) *model.Upload {
	next := *u
	next.Objects = append([]model.UploadObject(nil), u.Objects...)
	return &next
}

func uploadNextLease(u *model.Upload, now time.Time) time.Time {
	lease := now.Add(24 * time.Hour)
	if lease.After(u.MaximumExpiresAt) {
		lease = u.MaximumExpiresAt
	}
	if lease.Before(u.LeaseExpiresAt) {
		lease = u.LeaseExpiresAt
	}
	return lease
}

func uploadExpired(u *model.Upload, now time.Time) bool {
	return !u.LeaseExpiresAt.After(now) || !u.MaximumExpiresAt.After(now)
}

func (r *uploadRepo) Complete(ctx context.Context, owner, id string, objects []model.UploadObject) (*model.Upload, error) {
	if err := validateUploadIdentity(owner, id); err != nil {
		return nil, err
	}
	if err := validateUploadObjects(owner, id, objects, true); err != nil {
		return nil, err
	}
	objects = append([]model.UploadObject(nil), objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Kind == "video" && objects[j].Kind != "video" })
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	return r.mutate(ctx, owner, id, func(u *model.Upload) (*model.Upload, bson.M, bson.M, error) {
		if !uploadObjectsEqual(u.Objects, objects, false) {
			return nil, nil, nil, ErrUploadInvalid
		}
		if u.Status == "ready" {
			if !uploadObjectsEqual(u.Objects, objects, true) {
				return nil, nil, nil, ErrUploadConflict
			}
			return u, nil, nil, nil
		}
		if u.Status != "uploading" {
			return nil, nil, nil, ErrUploadConflict
		}
		if !u.MaximumExpiresAt.After(now) {
			return nil, nil, nil, ErrUploadExpired
		}
		next := cloneUpload(u)
		next.Status = "ready"
		next.Objects = objects
		next.LeaseExpiresAt = uploadNextLease(u, now)
		return next, bson.M{"status": "ready", "objects": objects, "lease_expires_at": next.LeaseExpiresAt}, bson.M{"maximum_expires_at": bson.M{"$gt": now}}, nil
	})
}

func (r *uploadRepo) Fail(ctx context.Context, owner, id string) error {
	if err := validateUploadIdentity(owner, id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.mutate(ctx, owner, id, func(u *model.Upload) (*model.Upload, bson.M, bson.M, error) {
		if u.Status == "failed" {
			return u, nil, nil, nil
		}
		if u.Status != "uploading" {
			return nil, nil, nil, ErrUploadConflict
		}
		next := cloneUpload(u)
		next.Status = "failed"
		return next, bson.M{"status": "failed"}, nil, nil
	})
	return err
}

func (r *uploadRepo) Renew(ctx context.Context, owner, id string, now time.Time) (*model.Upload, error) {
	if err := validateUploadIdentity(owner, id); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, ErrUploadInvalid
	}
	now = now.UTC().Truncate(time.Millisecond)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.mutate(ctx, owner, id, func(u *model.Upload) (*model.Upload, bson.M, bson.M, error) {
		if u.Status != "ready" {
			return nil, nil, nil, ErrUploadConflict
		}
		if uploadExpired(u, now) {
			return nil, nil, nil, ErrUploadExpired
		}
		lease := uploadNextLease(u, now)
		if lease.Equal(u.LeaseExpiresAt) {
			return u, nil, nil, nil
		}
		next := cloneUpload(u)
		next.LeaseExpiresAt = lease
		return next, bson.M{"lease_expires_at": lease}, bson.M{"lease_expires_at": bson.M{"$gt": now}, "maximum_expires_at": bson.M{"$gt": now}}, nil
	})
}

func (r *uploadRepo) Reserve(ctx context.Context, owner, id, key, hash string, now time.Time) (*model.Upload, error) {
	if validateUploadIdentity(owner, id) != nil || !validPublishKey(key) || !validPublishHash(hash) || now.IsZero() {
		return nil, ErrUploadInvalid
	}
	now = now.UTC().Truncate(time.Millisecond)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// These candidates stay fixed even if this invocation loses a known CAS.
	videoID, jobID := primitive.NewObjectID().Hex(), uuid.NewString()
	return r.mutate(ctx, owner, id, func(u *model.Upload) (*model.Upload, bson.M, bson.M, error) {
		if u.Status == "reserved" || u.Status == "claimed" {
			if u.PublishKey != key || u.PayloadHash != hash {
				return nil, nil, nil, ErrUploadConflict
			}
			return u, nil, nil, nil
		}
		if u.Status != "ready" {
			return nil, nil, nil, ErrUploadConflict
		}
		if uploadExpired(u, now) {
			return nil, nil, nil, ErrUploadExpired
		}
		next := cloneUpload(u)
		next.Status = "reserved"
		next.PublishKey, next.PayloadHash, next.VideoID, next.JobID = key, hash, videoID, jobID
		return next, bson.M{"status": "reserved", "publish_key": key, "payload_hash": hash, "video_id": videoID, "job_id": jobID}, bson.M{"lease_expires_at": bson.M{"$gt": now}, "maximum_expires_at": bson.M{"$gt": now}}, nil
	})
}

func (r *uploadRepo) MarkClaimed(ctx context.Context, owner, id, key, hash, videoID string) error {
	if validateUploadIdentity(owner, id) != nil || !validPublishKey(key) || !validPublishHash(hash) || !validUploadVideoID(videoID) {
		return ErrUploadInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.mutate(ctx, owner, id, func(u *model.Upload) (*model.Upload, bson.M, bson.M, error) {
		if (u.Status != "reserved" && u.Status != "claimed") || u.PublishKey != key || u.PayloadHash != hash || u.VideoID != videoID {
			return nil, nil, nil, ErrUploadConflict
		}
		if u.Status == "claimed" {
			return u, nil, nil, nil
		}
		next := cloneUpload(u)
		next.Status = "claimed"
		return next, bson.M{"status": "claimed"}, bson.M{"publish_key": key, "payload_hash": hash, "video_id": videoID}, nil
	})
	return err
}

func (r *uploadRepo) EnsureIndexes(ctx context.Context) error {
	if r == nil || r.col == nil {
		return ErrUploadUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The canonical UUID is _id, whose Mongo primary index is already unique.
	// Business records are never removed by TTL indexes.
	_, err := r.col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "owner_id", Value: 1}, {Key: "status", Value: 1}, {Key: "lease_expires_at", Value: 1}}, Options: options.Index().SetName("upload_owner_status_lease")},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "maximum_expires_at", Value: 1}}, Options: options.Index().SetName("upload_status_maximum")},
	})
	if err != nil {
		return ErrUploadUnavailable
	}
	return nil
}
