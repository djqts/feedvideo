package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
)

// UploadMetadata contains verified object facts without a video model dependency.
type UploadMetadata struct {
	Size        int64
	ContentType string
	SHA256      string
	Version     string
}

// VerifiedStorage is required by durable uploads. The legacy Storage stays unchanged.
type VerifiedStorage interface {
	UploadVerified(context.Context, string, string, io.ReadSeeker, int64, string) (*UploadMetadata, error)
	StatUploadObject(context.Context, string, string, string) (*UploadMetadata, error)
}

// ErrObjectVerification marks a definite content or metadata mismatch. Transport
// failures are not this error: an unsuccessful response can still have stored bytes.
var ErrObjectVerification = errors.New("object verification failed")

func (m *MinioStorage) StatUploadObject(ctx context.Context, bucket, key, version string) (*UploadMetadata, error) {
	if m == nil || m.client == nil {
		return nil, errors.New("object storage unavailable")
	}
	info, err := m.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{VersionID: version})
	if err != nil {
		return nil, fmt.Errorf("stat upload object: %w", err)
	}
	hash := info.Metadata.Get("X-Amz-Meta-Sha256")
	if hash == "" {
		hash = info.UserMetadata["sha256"]
	}
	metadata := &UploadMetadata{Size: info.Size, ContentType: info.ContentType, SHA256: hash, Version: info.VersionID}
	if !validUploadMetadata(metadata) || (version != "" && metadata.Version != version) {
		return nil, ErrObjectVerification
	}
	return metadata, nil
}

// UploadVerified streams one hash pass and one PUT pass. It does not retry a
// consumed reader or delete targets after an uncertain object-store outcome.
func (m *MinioStorage) UploadVerified(ctx context.Context, bucket, key string, reader io.ReadSeeker, size int64, contentType string) (*UploadMetadata, error) {
	if m == nil || m.client == nil {
		return nil, errors.New("object storage unavailable")
	}
	if reader == nil || size <= 0 || contentType == "" {
		return nil, ErrObjectVerification
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek upload: %w", err)
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(uploadContextReader{ctx: ctx, reader: reader}, size+1))
	if err != nil {
		return nil, fmt.Errorf("hash upload: %w", err)
	}
	if n != size {
		return nil, ErrObjectVerification
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind upload: %w", err)
	}
	putDigest := sha256.New()
	putReader := &uploadCountReader{reader: io.TeeReader(uploadContextReader{ctx: ctx, reader: reader}, putDigest)}
	info, err := m.client.PutObject(ctx, bucket, key, putReader, size, minio.PutObjectOptions{ContentType: contentType, UserMetadata: map[string]string{"sha256": hash}, DisableMultipart: true, DisableContentSha256: true})
	if err != nil {
		return nil, fmt.Errorf("put verified object: %w", err)
	}
	if putReader.count != size || hex.EncodeToString(putDigest.Sum(nil)) != hash {
		return nil, ErrObjectVerification
	}
	metadata, err := m.StatUploadObject(ctx, bucket, key, info.VersionID)
	if err != nil {
		return nil, err
	}
	if metadata.Size != size || metadata.ContentType != contentType || metadata.SHA256 != hash {
		return nil, ErrObjectVerification
	}
	return metadata, nil
}

func validUploadMetadata(m *UploadMetadata) bool {
	if m == nil || m.Size <= 0 || m.ContentType == "" || len(m.SHA256) != 64 || strings.ToLower(m.SHA256) != m.SHA256 {
		return false
	}
	_, err := hex.DecodeString(m.SHA256)
	return err == nil
}

type uploadContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r uploadContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

var _ VerifiedStorage = (*MinioStorage)(nil)

// ValidateUploadObject checks the registered immutable object facts at publish.
func (m *MinioStorage) ValidateUploadObject(ctx context.Context, bucket, key, version, checksum string, size int64) error {
	metadata, err := m.StatUploadObject(ctx, bucket, key, version)
	if err != nil {
		return err
	}
	if metadata.Size != size || metadata.SHA256 != checksum {
		return ErrObjectVerification
	}
	return nil
}

type uploadCountReader struct {
	reader  io.Reader
	count   int64
	started bool
}

func (r *uploadCountReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count += int64(n)
	return n, err
}

// MinIO's hookReader promotes io.Seeker even for non-seekable sources. Allow
// its initial positioning, but explicitly reject a second attempt so an
// uncertain PUT cannot blindly replay a consumed body.
func (r *uploadCountReader) Seek(offset int64, whence int) (int64, error) {
	if r.started || r.count != 0 || offset != 0 || whence != io.SeekStart {
		return 0, errors.New("upload body cannot be replayed")
	}
	r.started = true
	return 0, nil
}
