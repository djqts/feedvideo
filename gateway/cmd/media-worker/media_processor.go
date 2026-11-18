package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"feedvideo.local/video/model"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

const mediaMaxSourceBytes int64 = 500 * 1024 * 1024

type mediaProcessError struct {
	Code      string
	Retryable bool
	Err       error
}

func (e *mediaProcessError) Error() string { return e.Code }
func (e *mediaProcessError) Unwrap() error { return e.Err }

type mediaObjectInfo struct {
	Size    int64
	Version string
	SHA256  string
}
type mediaObjects interface {
	Stat(context.Context, string, string, string) (mediaObjectInfo, error)
	Open(context.Context, string, string, string) (io.ReadCloser, error)
	Put(context.Context, string, string, io.Reader, int64, string, string) (mediaObjectInfo, error)
	Remove(context.Context, string, string, string) error
}
type mediaCommands interface {
	Probe(context.Context, string) (probeResult, error)
	Transcode(context.Context, string, string, int) error
	Thumbnail(context.Context, string, string) error
}
type persistentMediaProcessor struct {
	objects     mediaObjects
	commands    mediaCommands
	videoBucket string
	coverBucket string
}

func newPersistentMediaProcessor(objects mediaObjects, commands mediaCommands, videoBucket, coverBucket string) *persistentMediaProcessor {
	return &persistentMediaProcessor{objects: objects, commands: commands, videoBucket: videoBucket, coverBucket: coverBucket}
}

func processFailure(code string, retryable bool, err error) error {
	return &mediaProcessError{Code: code, Retryable: retryable, Err: err}
}
func safeMediaBucket(bucket string) bool {
	return bucket != "" && !strings.ContainsAny(bucket, "/\\%?#") && strings.TrimSpace(bucket) == bucket
}
func safeMediaObject(key string) bool {
	return key != "" && !strings.HasPrefix(key, "/") && !strings.HasSuffix(key, "/") &&
		key != "." && key != ".." && !strings.HasPrefix(key, "../") &&
		!strings.ContainsAny(key, "\\%?#") && path.Clean(key) == key
}

func (p *persistentMediaProcessor) Process(ctx context.Context, lease *model.MediaLease) (result *model.MediaResult, processErr error) {
	result = &model.MediaResult{}
	defer func() {
		if err := ctx.Err(); err != nil {
			processErr = err
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if lease == nil || p.objects == nil || p.commands == nil {
		return result, processFailure("internal_error", true, errors.New("media processor unavailable"))
	}
	jobID, jobErr := uuid.Parse(lease.JobID)
	if lease.VideoID.IsZero() || lease.ContentVersion < 1 || lease.LeaseToken < 1 || jobErr != nil || jobID.Version() != 4 || jobID.String() != lease.JobID {
		return result, processFailure("invalid_media", false, errors.New("invalid media lease identity"))
	}
	// The publish boundary has authenticated and normalized this DB reference.
	// Never use a URL or source key from a Redis hint.
	if !safeMediaBucket(p.videoBucket) || !safeMediaBucket(p.coverBucket) || lease.SourceBucket != p.videoBucket || !safeMediaObject(lease.SourceObject) {
		return result, processFailure("source_download_failed", false, errors.New("invalid database media source"))
	}
	tmpDir, err := os.MkdirTemp("", "media_job_*")
	if err != nil {
		return result, processFailure("internal_error", true, err)
	}
	defer os.RemoveAll(tmpDir)
	src := filepath.Join(tmpDir, "source")
	if err := p.download(ctx, lease, src); err != nil {
		return result, processFailure("source_download_failed", true, err)
	}

	probe, err := p.commands.Probe(ctx, src)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, processFailure("invalid_media", false, err)
	}
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return result, processFailure("invalid_media", false, errors.New("invalid media duration"))
	}
	for _, stream := range probe.Streams {
		if stream.Width > 0 && stream.Height > 0 {
			result.Width, result.Height = stream.Width, stream.Height
			break
		}
	}
	if result.Width <= 0 || result.Height <= 0 {
		return result, processFailure("invalid_media", false, errors.New("video stream missing"))
	}
	result.Duration = duration
	prefix := model.OutputPrefix(lease)
	for _, height := range []int{480, 720} {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		name := strconv.Itoa(height) + "p.mp4"
		local := filepath.Join(tmpDir, name)
		if err := p.commands.Transcode(ctx, src, local, height); err != nil {
			return result, processFailure("transcode_failed", true, err)
		}
		artifact, err := p.upload(ctx, p.videoBucket, prefix+name, strconv.Itoa(height)+"p", local, "video/mp4")
		// Include the attempted object even when Put/Stat is uncertain, so a
		// pre-commit failure can reclaim the isolated token path.
		if artifact.ObjectKey != "" {
			result.Manifest = append(result.Manifest, artifact)
		}
		if err != nil {
			return result, processFailure("upload_failed", true, err)
		}
	}
	if lease.CoverURL == "" {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		local := filepath.Join(tmpDir, "cover.jpg")
		if err := p.commands.Thumbnail(ctx, src, local); err != nil {
			return result, processFailure("cover_failed", true, err)
		}
		artifact, err := p.upload(ctx, p.coverBucket, prefix+"cover.jpg", "cover", local, "image/jpeg")
		if artifact.ObjectKey != "" {
			result.Manifest = append(result.Manifest, artifact)
		}
		if err != nil {
			return result, processFailure("upload_failed", true, err)
		}
	}
	if err := model.ValidateMediaResult(lease, result); err != nil {
		return result, processFailure("invalid_media", false, err)
	}
	return result, nil
}

func (p *persistentMediaProcessor) download(ctx context.Context, lease *model.MediaLease, destination string) error {
	info, err := p.objects.Stat(ctx, lease.SourceBucket, lease.SourceObject, "")
	if err != nil {
		return err
	}
	if info.Size <= 0 || info.Size > mediaMaxSourceBytes {
		return errors.New("source size outside media limit")
	}
	// Pin the source version when storage supplies one. A changing unversioned
	// object is also rejected if its streamed byte count differs from Stat.
	source, err := p.objects.Open(ctx, lease.SourceBucket, lease.SourceObject, info.Version)
	if err != nil {
		return err
	}
	defer source.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	size, copyErr := io.Copy(output, io.LimitReader(mediaContextReader{ctx: ctx, reader: source}, info.Size+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if size != info.Size || size > mediaMaxSourceBytes {
		return errors.New("source streamed size mismatch")
	}
	return nil
}

func (p *persistentMediaProcessor) upload(ctx context.Context, bucket, key, kind, filename, contentType string) (model.MediaArtifact, error) {
	file, err := os.Open(filename)
	if err != nil {
		return model.MediaArtifact{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 {
		return model.MediaArtifact{}, errors.Join(err, errors.New("empty output file"))
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, mediaContextReader{ctx: ctx, reader: file}); err != nil {
		return model.MediaArtifact{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return model.MediaArtifact{}, err
	}
	artifact := model.MediaArtifact{Kind: kind, Bucket: bucket, ObjectKey: key, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}
	uploaded, err := p.objects.Put(ctx, bucket, key, mediaContextReader{ctx: ctx, reader: file}, artifact.Size, contentType, artifact.SHA256)
	artifact.ObjectVersion = uploaded.Version
	if err != nil {
		return artifact, err
	}
	if uploaded.Size != artifact.Size {
		return artifact, errors.New("uploaded size mismatch")
	}
	observed, err := p.objects.Stat(ctx, bucket, key, uploaded.Version)
	if err != nil {
		return artifact, err
	}
	if observed.Size != artifact.Size || (uploaded.Version != "" && observed.Version != uploaded.Version) {
		return artifact, errors.New("stored output size or version mismatch")
	}
	if !strings.EqualFold(observed.SHA256, artifact.SHA256) {
		return artifact, errors.New("stored output SHA-256 metadata mismatch")
	}
	if artifact.ObjectVersion == "" {
		artifact.ObjectVersion = observed.Version
	}
	return artifact, nil
}

func (p *persistentMediaProcessor) Cleanup(parent context.Context, lease *model.MediaLease, result *model.MediaResult) error {
	if result == nil || len(result.Manifest) == 0 {
		return nil
	}
	if lease == nil || lease.VideoID.IsZero() || lease.ContentVersion < 1 || lease.LeaseToken < 1 {
		return errors.New("invalid cleanup lease")
	}
	jobID, err := uuid.Parse(lease.JobID)
	if err != nil || jobID.Version() != 4 || jobID.String() != lease.JobID {
		return errors.New("invalid cleanup job")
	}
	prefix := model.OutputPrefix(lease)
	// Validate the entire partial manifest before deleting any object.
	for _, artifact := range result.Manifest {
		bucket := p.videoBucket
		if artifact.Kind == "cover" {
			bucket = p.coverBucket
		}
		if (artifact.Kind != "480p" && artifact.Kind != "720p" && artifact.Kind != "cover") ||
			artifact.Bucket != bucket || !safeMediaBucket(bucket) ||
			!safeMediaObject(artifact.ObjectKey) || !strings.HasPrefix(artifact.ObjectKey, prefix) || artifact.ObjectKey == strings.TrimSuffix(prefix, "/") {
			return errors.New("cleanup artifact outside this media token")
		}
	}
	ctx, cancel := context.WithTimeout(parent, mediaStoreBudget)
	defer cancel()
	var cleanupErr error
	seen := map[string]bool{}
	for _, artifact := range result.Manifest {
		key := artifact.Bucket + "/" + artifact.ObjectKey + "/" + artifact.ObjectVersion
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := p.objects.Remove(ctx, artifact.Bucket, artifact.ObjectKey, artifact.ObjectVersion); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

type mediaContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r mediaContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type minioMediaObjects struct{ client *minio.Client }

func (m *minioMediaObjects) Stat(ctx context.Context, bucket, key, version string) (mediaObjectInfo, error) {
	info, err := m.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{VersionID: version})
	if err != nil {
		return mediaObjectInfo{}, err
	}
	hash := info.UserMetadata["sha256"]
	if hash == "" {
		hash = info.Metadata.Get("X-Amz-Meta-Sha256")
	}
	return mediaObjectInfo{Size: info.Size, Version: info.VersionID, SHA256: hash}, nil
}
func (m *minioMediaObjects) Open(ctx context.Context, bucket, key, version string) (io.ReadCloser, error) {
	return m.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{VersionID: version})
}
func (m *minioMediaObjects) Put(ctx context.Context, bucket, key string, input io.Reader, size int64, contentType, hash string) (mediaObjectInfo, error) {
	info, err := m.client.PutObject(ctx, bucket, key, input, size, minio.PutObjectOptions{ContentType: contentType, UserMetadata: map[string]string{"sha256": hash}})
	return mediaObjectInfo{Size: info.Size, Version: info.VersionID}, err
}
func (m *minioMediaObjects) Remove(ctx context.Context, bucket, key, version string) error {
	return m.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{VersionID: version})
}

type ffmpegMediaCommands struct{ ffmpegBin, ffprobeBin string }

func (c *ffmpegMediaCommands) Probe(ctx context.Context, source string) (probeResult, error) {
	return probeVideo(ctx, c.ffprobeBin, source)
}
func (c *ffmpegMediaCommands) Transcode(ctx context.Context, source, destination string, height int) error {
	return runFFmpegHeight(ctx, c.ffmpegBin, source, destination, height)
}
func (c *ffmpegMediaCommands) Thumbnail(ctx context.Context, source, destination string) error {
	return runThumbnail(ctx, c.ffmpegBin, source, destination)
}
