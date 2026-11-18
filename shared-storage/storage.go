package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	sharedconfig "feedvideo.local/shared-config"
)

// Storage 定义文件上传与 bucket 管理的抽象接口。
type Storage interface {
	// Upload 上传文件到指定 bucket，返回相对路径 URL（格式: /storage/<bucket>/<objectName>）。
	Upload(ctx context.Context, bucket, objectName string,
		reader io.Reader, size int64, contentType string) (url string, err error)

	// EnsureBuckets 确保指定 buckets 存在并设置为 public-read。幂等：重复调用不会出错。
	EnsureBuckets(ctx context.Context, buckets []string) error

	// Delete removes an object (used by upload orphan reclamation).
	Delete(ctx context.Context, bucket, objectName string) error
}

// MinioStorage 基于 MinIO SDK 的 Storage 实现。
type MinioStorage struct {
	client *minio.Client
}

// NewMinioStorage 创建 MinIO 客户端。cfg 中的 UseSSL 为 false 时使用 HTTP。
func NewMinioStorage(cfg *sharedconfig.StorageConfig) (*MinioStorage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client init: %w", err)
	}
	return &MinioStorage{client: client}, nil
}

// Upload 上传文件并返回相对路径 URL。
func (m *MinioStorage) Upload(ctx context.Context, bucket, objectName string,
	reader io.Reader, size int64, contentType string) (string, error) {

	_, err := m.client.PutObject(ctx, bucket, objectName, reader, size,
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("put object: %w", err)
	}
	return fmt.Sprintf("/storage/%s/%s", bucket, objectName), nil
}

// Delete removes an object from MinIO.
func (m *MinioStorage) Delete(ctx context.Context, bucket, objectName string) error {
	if err := m.client.RemoveObject(ctx, bucket, objectName, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object %s/%s: %w", bucket, objectName, err)
	}
	return nil
}

// ValidateMediaSource checks the existence of a server-normalized source reference.
// The video service owns bucket and author-prefix validation.
func (m *MinioStorage) ValidateMediaSource(ctx context.Context, bucket, objectName string) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("media source storage unavailable")
	}
	info, err := m.client.StatObject(ctx, bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("stat media source: %w", err)
	}
	if info.Size <= 0 {
		return fmt.Errorf("media source is empty")
	}
	return nil
}

// Ping 校验 MinIO 连通性（用于 /health 探测）。
func (m *MinioStorage) Ping(ctx context.Context) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("minio client is nil")
	}
	if _, err := m.client.ListBuckets(ctx); err != nil {
		return fmt.Errorf("minio ping: %w", err)
	}
	return nil
}

// EnsureBuckets 确保指定 buckets 存在，不存在则创建并设置 public-read policy。
// 最多重试 3 次，每次间隔 500ms。bucket 已存在视为幂等（非错误）。
func (m *MinioStorage) EnsureBuckets(ctx context.Context, buckets []string) error {
	publicReadPolicy := fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {"AWS": ["*"]},
      "Action": ["s3:GetObject"],
      "Resource": ["arn:aws:s3:::%s/*"]
    }
  ]
}`, "%s")

	for _, bucket := range buckets {
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				time.Sleep(500 * time.Millisecond)
			}

			err := m.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
			if err != nil {
				// Check if bucket already exists (idempotent)
				exists, errCheck := m.client.BucketExists(ctx, bucket)
				if errCheck == nil && exists {
					log.Printf("[storage] bucket %q already exists, skip creation", bucket)
					err = nil
				}
				if err != nil {
					lastErr = err
					continue
				}
			} else {
				log.Printf("[storage] bucket %q created", bucket)
			}

			// Set public-read policy
			policyStr := fmt.Sprintf(publicReadPolicy, bucket)
			err = m.client.SetBucketPolicy(ctx, bucket, policyStr)
			if err != nil {
				lastErr = fmt.Errorf("set bucket policy for %q: %w", bucket, err)
				continue
			}
			log.Printf("[storage] bucket %q policy set to public-read", bucket)

			lastErr = nil
			break
		}
		if lastErr != nil {
			return fmt.Errorf("ensure bucket %q after retries: %w", bucket, lastErr)
		}
	}
	return nil
}

// Compile-time check that MinioStorage implements Storage.
var _ Storage = (*MinioStorage)(nil)
