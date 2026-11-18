package handler

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	config "feedvideo.local/shared-config"
	storage "feedvideo.local/storage"
	"feedvideo.local/video/model"
	"feedvideo.local/video/orphan"
	"feedvideo.local/video/repo"
)

// UploadHandler handles multipart file uploads to MinIO.
type UploadHandler struct {
	uploads           repo.UploadStore
	uploadsConfigured bool
	storage           storage.Storage
	cfg               *config.StorageConfig
	tracker           *orphan.Tracker
	metrics           UploadMetrics
	onStatus          func(status string) // 媒体状态计数回调（P3.1：uploaded/validated）
}

// UploadMetrics 上传生命周期指标的最小接口。
type UploadMetrics interface {
	IncUploadSuccess()
	IncUploadFailure()
	IncFallback(target string)
}

// NewUploadHandler creates a new UploadHandler.
func NewUploadHandler(s storage.Storage, cfg *config.StorageConfig, tracker ...*orphan.Tracker) *UploadHandler {
	h := &UploadHandler{storage: s, cfg: cfg, onStatus: func(string) {}}
	if len(tracker) > 0 {
		h.tracker = tracker[0]
	}
	return h
}

// WithMetrics 挂接上传指标（P1.2）。
func (h *UploadHandler) WithMetrics(m UploadMetrics) *UploadHandler {
	h.metrics = m
	return h
}

// WithStatusHook 挂接媒体状态计数回调（P3.1）。
func (h *UploadHandler) WithStatusHook(hook func(status string)) *UploadHandler {
	if hook != nil {
		h.onStatus = hook
	}
	return h
}

// Upload handles POST /api/v1/upload — multipart video + optional cover upload.
// Upload godoc
// @Summary 上传视频/封面文件
// @Description 上传到 MinIO 并返回 /storage/... URL；视频≤500MB(mp4/mov/webm)，封面≤10MB(jpg/png/webp)
// @Tags 文件上传
// @Accept multipart/form-data
// @Produce json
// @Param video formData file true "视频文件"
// @Param cover formData file false "封面文件"
// @Success 200 {object} map[string]interface{} "上传成功 {video_url, cover_url}"
// @Failure 400 {object} map[string]string "文件校验失败"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 500 {object} map[string]string "上传失败"
// @Router /api/v1/upload [post]
func (h *UploadHandler) Upload(c *gin.Context) {
	if h.uploadsConfigured {
		h.uploadRegistered(c)
		return
	}
	// 1. Extract authorID from JWT
	authorID, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "message": "unauthorized"})
		return
	}
	aid, ok := authorID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "message": "unauthorized"})
		return
	}

	// 2. Read video file (required)
	videoFH, err := c.FormFile("video")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "video file is required"})
		return
	}

	// Validate video
	if err := h.validateFile(videoFH, h.cfg.AllowedVideoTypes, h.cfg.MaxVideoSize); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": err.Error()})
		if h.metrics != nil {
			h.metrics.IncUploadFailure()
		}
		return
	}

	// Generate object name
	ts := time.Now().Unix()
	ext := strings.ToLower(filepath.Ext(videoFH.Filename))
	objName := fmt.Sprintf("%s/%d-%s%s", aid, ts, uuid.New().String(), ext)

	// Open video file
	vf, err := videoFH.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "message": "failed to read video file"})
		if h.metrics != nil {
			h.metrics.IncUploadFailure()
		}
		return
	}
	defer vf.Close()

	// Upload video
	videoURL, err := h.storage.Upload(c.Request.Context(), h.cfg.VideoBucket, objName, vf, videoFH.Size, videoFH.Header.Get("Content-Type"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "message": "upload failed"})
		if h.metrics != nil {
			h.metrics.IncUploadFailure()
		}
		return
	}
	if h.metrics != nil {
		h.metrics.IncUploadSuccess()
	}
	// 上传对象进入 uploaded；通过本函数全部校验后视为 validated（P3.1）
	h.onStatus("uploaded")
	h.onStatus("validated")
	if h.tracker != nil {
		if err := h.tracker.MarkPending(c.Request.Context(), h.cfg.VideoBucket, objName); err != nil {
			log.Printf("[upload] mark pending failed (orphan leak risk): bucket=%s object=%s err=%v", h.cfg.VideoBucket, objName, err)
		}
	}

	// 3. Optional cover
	coverURL := ""
	coverFH, err := c.FormFile("cover")
	if err == nil {
		if err := h.validateFile(coverFH, h.cfg.AllowedCoverTypes, h.cfg.MaxCoverSize); err != nil {
			// Cover validation failed — video already uploaded (orphan acceptable per design)
		} else {
			cext := strings.ToLower(filepath.Ext(coverFH.Filename))
			coverObjName := fmt.Sprintf("%s/%d-%s%s", aid, ts, uuid.New().String(), cext)
			cf, cfErr := coverFH.Open()
			if cfErr == nil && cf != nil {
				defer cf.Close()
				coverURL, _ = h.storage.Upload(c.Request.Context(), h.cfg.CoverBucket, coverObjName, cf, coverFH.Size, coverFH.Header.Get("Content-Type"))
				if coverURL != "" && h.tracker != nil {
					if err := h.tracker.MarkPending(c.Request.Context(), h.cfg.CoverBucket, coverObjName); err != nil {
						log.Printf("[upload] mark pending failed (orphan leak risk): bucket=%s object=%s err=%v", h.cfg.CoverBucket, coverObjName, err)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
		"video_url": videoURL,
		"cover_url": coverURL,
	}})
}

// validateFile performs Content-Type + extension double-lock validation.
func (h *UploadHandler) validateFile(fh *multipart.FileHeader, allowedTypes []string, maxSize int64) error {
	ct := fh.Header.Get("Content-Type")

	// 拒绝空文件
	if fh.Size <= 0 {
		return fmt.Errorf("file is empty")
	}

	// Check size
	if fh.Size > maxSize {
		return fmt.Errorf("file size %d exceeds limit %d", fh.Size, maxSize)
	}

	// Check Content-Type is in whitelist
	typeAllowed := false
	for _, at := range allowedTypes {
		if ct == at {
			typeAllowed = true
			break
		}
	}
	if !typeAllowed {
		return fmt.Errorf("unsupported video format: %s", ct)
	}

	// Check extension
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if ext == "" {
		return fmt.Errorf("unsupported video extension: (none)")
	}

	// Check Content-Type matches extension via ExtByMime mapping
	expectedExt, ok := config.ExtByMime[ct]
	if !ok || ext != expectedExt {
		return fmt.Errorf("Content-Type %s does not match file extension %s", ct, ext)
	}

	return nil
}

// WithUploadStore enables the durable upload protocol, including fail-closed
// behavior when a configured dependency does not implement verified storage.
func (h *UploadHandler) WithUploadStore(s repo.UploadStore) *UploadHandler {
	h.uploads = s
	h.uploadsConfigured = true
	return h
}

func (h *UploadHandler) uploadRegistered(c *gin.Context) {
	owner, ok := uploadOwner(c)
	if !ok {
		uploadFailure(c, http.StatusUnauthorized)
		return
	}
	verified, ok := h.storage.(storage.VerifiedStorage)
	if !ok || h.uploads == nil || h.cfg == nil || h.cfg.MaxVideoSize <= 0 || h.cfg.MaxCoverSize <= 0 {
		uploadFailure(c, http.StatusServiceUnavailable)
		return
	}
	ctx := c.Request.Context()
	if ctx.Err() != nil {
		uploadFailure(c, http.StatusServiceUnavailable)
		return
	}
	videoLimit := minUploadLimit(h.cfg.MaxVideoSize, 500<<20)
	coverLimit := minUploadLimit(h.cfg.MaxCoverSize, 10<<20)
	c.Request.Body = http.MaxBytesReader(c.Writer, uploadBody{ctx: ctx, ReadCloser: c.Request.Body}, videoLimit+coverLimit+(1<<20))
	err := c.Request.ParseMultipartForm(1 << 20)
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err != nil || c.Request.MultipartForm == nil {
		if ctx.Err() != nil {
			uploadFailure(c, 503)
		} else {
			uploadFailure(c, 400)
		}
		return
	}
	form := c.Request.MultipartForm
	if len(form.File["video"]) != 1 || len(form.File["cover"]) > 1 {
		uploadFailure(c, 400)
		return
	}
	for name, files := range form.File {
		if name != "video" && name != "cover" && len(files) > 0 {
			uploadFailure(c, 400)
			return
		}
	}
	video := form.File["video"][0]
	if h.validateFile(video, h.cfg.AllowedVideoTypes, videoLimit) != nil {
		uploadFailure(c, 400)
		return
	}
	files := []*multipart.FileHeader{video}
	kinds := []string{"video"}
	buckets := []string{h.cfg.VideoBucket}
	if len(form.File["cover"]) == 1 {
		cover := form.File["cover"][0]
		if h.validateFile(cover, h.cfg.AllowedCoverTypes, coverLimit) != nil {
			uploadFailure(c, 400)
			return
		}
		files = append(files, cover)
		kinds = append(kinds, "cover")
		buckets = append(buckets, h.cfg.CoverBucket)
	}
	readers := make([]multipart.File, 0, len(files))
	for _, file := range files {
		reader, openErr := file.Open()
		if openErr != nil {
			uploadFailure(c, 400)
			return
		}
		readers = append(readers, reader)
		defer reader.Close()
	}
	id := uuid.NewString()
	objects := make([]model.UploadObject, len(files))
	for i, file := range files {
		objects[i] = model.UploadObject{Kind: kinds[i], Bucket: buckets[i], ObjectKey: owner + "/uploads/" + id + "/" + kinds[i] + strings.ToLower(filepath.Ext(file.Filename)), Size: file.Size, ContentType: file.Header.Get("Content-Type")}
	}
	pending := &model.Upload{ID: id, OwnerID: owner, Status: "uploading", Objects: objects, Version: 1, ContentVersion: 1}
	if err := h.uploads.Create(ctx, pending); err != nil {
		uploadStoreFailure(c, err)
		return
	}
	completed := append([]model.UploadObject(nil), objects...)
	for i, obj := range objects {
		metadata, writeErr := verified.UploadVerified(ctx, obj.Bucket, obj.ObjectKey, readers[i], obj.Size, obj.ContentType)
		if writeErr == nil && !validRegisteredMetadata(metadata, obj) {
			writeErr = storage.ErrObjectVerification
		}
		if writeErr != nil {
			if errors.Is(writeErr, storage.ErrObjectVerification) {
				_ = h.uploads.Fail(ctx, owner, id)
			}
			if h.metrics != nil {
				h.metrics.IncUploadFailure()
			}
			uploadFailure(c, 503)
			return
		}
		completed[i].SHA256 = metadata.SHA256
		completed[i].ObjectVersion = metadata.Version
	}
	ready, err := h.uploads.Complete(ctx, owner, id, completed)
	if err != nil {
		if h.metrics != nil {
			h.metrics.IncUploadFailure()
		}
		uploadStoreFailure(c, err)
		return
	}
	if ready == nil || ready.ID != id || ready.OwnerID != owner || ready.Status != "ready" || !reflect.DeepEqual(ready.Objects, completed) {
		uploadFailure(c, 503)
		return
	}
	if h.metrics != nil {
		h.metrics.IncUploadSuccess()
	}
	h.onStatus("uploaded")
	h.onStatus("validated")
	h.respondUpload(c, ready)
}

// GetUpload returns durable upload state without exposing reservation internals.
func (h *UploadHandler) GetUpload(c *gin.Context) {
	owner, id, ok := h.uploadRequestIdentity(c)
	if !ok {
		return
	}
	row, err := h.uploads.Get(c.Request.Context(), owner, id)
	if err != nil {
		uploadStoreFailure(c, err)
		return
	}
	if row == nil || row.OwnerID != owner || row.ID != id {
		uploadFailure(c, 404)
		return
	}
	h.respondUpload(c, row)
}

// RenewUpload requests the repository's bounded, non-shortening ready lease.
func (h *UploadHandler) RenewUpload(c *gin.Context) {
	owner, id, ok := h.uploadRequestIdentity(c)
	if !ok {
		return
	}
	row, err := h.uploads.Renew(c.Request.Context(), owner, id, time.Now().UTC())
	if err != nil {
		uploadStoreFailure(c, err)
		return
	}
	if row == nil || row.OwnerID != owner || row.ID != id {
		uploadFailure(c, 404)
		return
	}
	if row.Status != "ready" {
		uploadFailure(c, 409)
		return
	}
	h.respondUpload(c, row)
}
func (h *UploadHandler) uploadRequestIdentity(c *gin.Context) (string, string, bool) {
	owner, ok := uploadOwner(c)
	if !ok {
		uploadFailure(c, 401)
		return "", "", false
	}
	id := c.Param("id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		uploadFailure(c, 400)
		return "", "", false
	}
	if !h.uploadsConfigured || h.uploads == nil {
		uploadFailure(c, 503)
		return "", "", false
	}
	return owner, id, true
}
func uploadOwner(c *gin.Context) (string, bool) {
	value, exists := c.Get("userID")
	if !exists {
		return "", false
	}
	owner, ok := value.(string)
	if !ok {
		return "", false
	}
	n, err := strconv.ParseInt(owner, 10, 64)
	return owner, err == nil && n > 0 && strconv.FormatInt(n, 10) == owner
}
func (h *UploadHandler) respondUpload(c *gin.Context, row *model.Upload) {
	if row.Version <= 0 || row.ContentVersion <= 0 {
		uploadFailure(c, 503)
		return
	}
	data := gin.H{"upload_id": row.ID, "status": row.Status, "version": strconv.FormatInt(row.Version, 10), "content_version": strconv.FormatInt(row.ContentVersion, 10), "lease_expires_at": row.LeaseExpiresAt.UTC(), "maximum_expires_at": row.MaximumExpiresAt.UTC()}
	now := time.Now().UTC()
	if row.Status == "ready" && (!now.Before(row.LeaseExpiresAt) || !now.Before(row.MaximumExpiresAt)) {
		uploadFailure(c, 410)
		return
	}
	if row.Status == "ready" && now.Before(row.LeaseExpiresAt) && now.Before(row.MaximumExpiresAt) {
		videoURL, coverURL := "", ""
		for _, obj := range row.Objects {
			if obj.Kind == "video" {
				videoURL = "/storage/" + obj.Bucket + "/" + obj.ObjectKey
			} else if obj.Kind == "cover" {
				coverURL = "/storage/" + obj.Bucket + "/" + obj.ObjectKey
			}
		}
		data["video_url"] = videoURL
		data["cover_url"] = coverURL
	}
	c.JSON(200, gin.H{"code": 0, "message": "ok", "data": data})
}
func validRegisteredMetadata(m *storage.UploadMetadata, o model.UploadObject) bool {
	if m == nil || m.Size != o.Size || m.Size <= 0 || m.ContentType != o.ContentType || len(m.SHA256) != 64 || strings.ToLower(m.SHA256) != m.SHA256 {
		return false
	}
	_, err := hex.DecodeString(m.SHA256)
	return err == nil
}
func uploadStoreFailure(c *gin.Context, err error) {
	status := 503
	switch {
	case errors.Is(err, repo.ErrUploadInvalid):
		status = 400
	case errors.Is(err, repo.ErrUploadNotFound):
		status = 404
	case errors.Is(err, repo.ErrUploadConflict):
		status = 409
	case errors.Is(err, repo.ErrUploadExpired):
		status = 410
	}
	uploadFailure(c, status)
}
func uploadFailure(c *gin.Context, status int) {
	message := map[int]string{400: "invalid upload request", 401: "unauthorized", 404: "upload not found", 409: "upload conflict", 410: "upload expired", 503: "upload unavailable"}[status]
	c.JSON(status, gin.H{"code": status, "message": message})
}
func minUploadLimit(configured, hard int64) int64 {
	if configured < hard {
		return configured
	}
	return hard
}

type uploadBody struct {
	ctx context.Context
	io.ReadCloser
}

func (b uploadBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}
