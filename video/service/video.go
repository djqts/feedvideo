package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"feedvideo.local/video/model"
	"feedvideo.local/video/repo"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
)

// ErrForbidden 越权访问标记，handler 应映射为 403
var ErrForbidden = errors.New("forbidden: not the author")

// ErrBadRequest 参数校验失败标记，handler 应映射为 400
var ErrBadRequest = errors.New("bad request")

var ErrMediaUnavailable = errors.New("media storage unavailable")

// TopicNotifier 话题热榜更新接口（由 tag-topic 模块实现，gateway 注入）。
// video 不 import tag-topic，仅依赖此最小接口。
type TopicNotifier interface {
	IncrHot(ctx context.Context, tag string) error
}

type VideoService struct {
	videoRepo         repo.VideoRepo
	likeRepo          repo.LikeRepo
	collectRepo       repo.CollectRepo
	topicNotifier     TopicNotifier
	sensitive         SensitiveChecker
	assetCleaner      func(ctx context.Context, urls []string)
	mediaJobsEnabled  bool
	videoBucket       string
	sourceValidator   MediaSourceValidator
	mediaJobStore     repo.MediaJobStore
	uploadStore       repo.UploadStore
	publishStore      repo.PublishStore
	coverBucket       string
	uploadsConfigured bool
}

type MediaSourceValidator interface {
	ValidateMediaSource(context.Context, string, string) error
}

func (s *VideoService) ConfigureMediaJobs(enabled bool, videoBucket string, validator MediaSourceValidator) {
	s.mediaJobsEnabled, s.videoBucket, s.sourceValidator = enabled, videoBucket, validator
}

// ValidateMediaSourceReference accepts only canonical references owned by the authenticated author.
func ValidateMediaSourceReference(raw, bucket, author string) (string, error) {
	if bucket == "" || author == "" || strings.ContainsAny(bucket+author, "/\\%?#") || author == "." || author == ".." {
		return "", fmt.Errorf("%w: invalid media source owner", ErrBadRequest)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\\%?#") || u.Path != raw || path.Clean(raw) != raw {
		return "", fmt.Errorf("%w: noncanonical media source", ErrBadRequest)
	}
	prefix := "/storage/" + bucket + "/"
	object := strings.TrimPrefix(raw, prefix)
	if !strings.HasPrefix(raw, prefix) || !strings.HasPrefix(object, author+"/") || object == author+"/" || strings.HasSuffix(object, "/") {
		return "", fmt.Errorf("%w: unowned media source", ErrBadRequest)
	}
	return object, nil
}

func NewVideoService(videoRepo repo.VideoRepo, likeRepo repo.LikeRepo) *VideoService {
	return &VideoService{videoRepo: videoRepo, likeRepo: likeRepo}
}

// SetCollectRepo supplies the optional collection state for video details.
func (s *VideoService) SetCollectRepo(r repo.CollectRepo) { s.collectRepo = r }

// SetTopicNotifier 注入话题热榜通知器（gateway 注入，nil 安全）。
func (s *VideoService) SetTopicNotifier(n TopicNotifier) { s.topicNotifier = n }

// SetSensitiveChecker 注入内容敏感词检查器（可选）。
func (s *VideoService) SetSensitiveChecker(c SensitiveChecker) {
	s.sensitive = c
}

// SetAssetCleaner 注入对象存储清理器（P3.3：删除视频时异步清理 MinIO 资产）。
func (s *VideoService) SetAssetCleaner(fn func(ctx context.Context, urls []string)) {
	s.assetCleaner = fn
}

// ——— Publish / CRUD ———

type PublishReq struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Music       string   `json:"music"`
	CoverURL    string   `json:"cover_url"`
	VideoURL    string   `json:"video_url"`
	Longitude   float64  `json:"longitude"`
	Latitude    float64  `json:"latitude"`
	AuthorID    string   `json:"-"` // 由 handler 从 JWT 注入，非客户端字段
	UploadID    string   `json:"upload_id,omitempty"`
	PublishKey  string   `json:"-"`
}

type PublishResp struct {
	VideoID        string `json:"video_id"`
	MediaStatus    string `json:"media_status,omitempty"`
	JobID          string `json:"job_id,omitempty"`
	ContentVersion int64  `json:"content_version,omitempty"`
	Replayed       bool   `json:"-"`
}

func (s *VideoService) Publish(ctx context.Context, req *PublishReq) (*PublishResp, error) {
	if req == nil {
		return nil, ErrBadRequest
	}
	if s.uploadsConfigured {
		return s.publishConfigured(ctx, req)
	}
	if req.UploadID != "" {
		return nil, ErrMediaUnavailable
	}
	if req.Title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrBadRequest)
	}
	if req.VideoURL == "" {
		return nil, fmt.Errorf("%w: video_url is required", ErrBadRequest)
	}
	if l := utf8.RuneCountInString(req.Title); l > 100 {
		return nil, fmt.Errorf("%w: title too long (max 100)", ErrBadRequest)
	}
	if l := utf8.RuneCountInString(req.Description); l > 2000 {
		return nil, fmt.Errorf("%w: description too long (max 2000)", ErrBadRequest)
	}
	if len(req.Tags) > 10 {
		return nil, fmt.Errorf("%w: too many tags (max 10)", ErrBadRequest)
	}
	for _, tag := range req.Tags {
		if utf8.RuneCountInString(tag) > 30 {
			return nil, fmt.Errorf("%w: tag too long (max 30)", ErrBadRequest)
		}
	}
	if s.sensitive != nil {
		hit, err := s.sensitive.ContainsSensitive(ctx, req.Title+" "+req.Description)
		if err == nil && hit {
			return nil, fmt.Errorf("%w: content contains sensitive words", ErrBadRequest)
		}
	}

	video := &model.Video{
		Title:           req.Title,
		Description:     req.Description,
		Tags:            req.Tags,
		Music:           req.Music,
		CoverURL:        req.CoverURL,
		VideoURL:        req.VideoURL,
		AuthorID:        req.AuthorID,
		Status:          "pending",
		MediaStatus:     "queued", // 上传对象已就绪，等待 media-worker 转码（P3.1）
		TranscodeStatus: "pending",
	}
	if s.mediaJobsEnabled {
		object, err := ValidateMediaSourceReference(req.VideoURL, s.videoBucket, req.AuthorID)
		if err != nil {
			return nil, err
		}
		if s.sourceValidator == nil {
			return nil, fmt.Errorf("%w: source validator is not configured", ErrMediaUnavailable)
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.sourceValidator.ValidateMediaSource(checkCtx, s.videoBucket, object)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("%w: validate source: %w", ErrMediaUnavailable, err)
		}
		now := time.Now().UTC()
		video.ContentVersion = 1
		video.MediaJob = &model.MediaJob{JobID: uuid.NewString(), ContentVersion: 1, Status: "queued", SourceKind: "object", SourceBucket: s.videoBucket, SourceObject: object, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	}

	if req.Longitude != 0 || req.Latitude != 0 {
		video.Location = &model.Geo{
			Type:        "Point",
			Coordinates: []float64{req.Longitude, req.Latitude},
		}
	}

	if err := s.videoRepo.Create(ctx, video); err != nil {
		if s.mediaJobsEnabled {
			return nil, fmt.Errorf("%w: publish: %w", ErrMediaUnavailable, err)
		}
		return nil, fmt.Errorf("publish video: %w", err)
	}

	// 异步更新话题热榜（单个 goroutine 顺序遍历 tags，避免按 tag 无界起 goroutine）
	if len(video.Tags) > 0 {
		tags := make([]string, len(video.Tags))
		copy(tags, video.Tags)
		go func() {
			defer func() { _ = recover() }()
			for _, t := range tags {
				if s.topicNotifier != nil {
					if err := s.topicNotifier.IncrHot(context.Background(), t); err != nil {
						slog.Warn("incr topic hot failed", "tag", t, "error", err)
					}
				}
			}
		}()
	}

	resp := &PublishResp{VideoID: video.ID.Hex()}
	if video.MediaJob != nil {
		resp.MediaStatus = video.MediaJob.Status
		resp.JobID = video.MediaJob.JobID
		resp.ContentVersion = video.ContentVersion
	}
	return resp, nil
}

type VideoDetail struct {
	*model.Video
	Stats          *model.VideoStats `json:"stats"`
	LikeStateKnown bool              `json:"like_state_known"`
	Liked          bool              `json:"liked"`
	IsLiked        bool              `json:"is_liked"`
	CollectCount   int64             `json:"collect_count"`
	ShareCount     int64             `json:"share_count"`
	IsCollected    bool              `json:"is_collected"`
}

type UpdateVideoReq struct {
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
	Music       *string  `json:"music"`
	CoverURL    *string  `json:"cover_url"`
}

func (s *VideoService) UpdateVideo(ctx context.Context, videoID string, req *UpdateVideoReq, userID string) error {
	// 越权防护：仅作者可更新
	v, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("get video: %w", err)
	}
	if v.AuthorID != userID {
		return ErrForbidden
	}
	update := bson.M{}
	if req.Title != nil {
		update["title"] = *req.Title
	}
	if req.Description != nil {
		update["description"] = *req.Description
	}
	if req.Tags != nil {
		update["tags"] = req.Tags
	}
	if req.Music != nil {
		update["music"] = *req.Music
	}
	if req.CoverURL != nil {
		update["cover_url"] = *req.CoverURL
	}
	if len(update) == 0 {
		return fmt.Errorf("no fields to update")
	}
	return s.videoRepo.Update(ctx, videoID, update)
}

func (s *VideoService) DeleteVideo(ctx context.Context, videoID, userID string) error {
	// 越权防护：仅作者可删除
	v, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("get video: %w", err)
	}
	if v.AuthorID != userID {
		return ErrForbidden
	}
	if err := s.videoRepo.SoftDelete(ctx, videoID); err != nil {
		return err
	}
	// P3.3：异步清理 MinIO 对象（原片/转码/封面/缩略图）
	if s.assetCleaner != nil {
		var urls []string
		for _, u := range []string{v.VideoURL, v.CoverURL, v.TranscodedURL, v.TranscodedURL720} {
			if u != "" {
				urls = append(urls, u)
			}
		}
		go func() {
			defer func() { _ = recover() }()
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.assetCleaner(cctx, urls)
		}()
	}
	return nil
}

// ——— Stats ———

func (s *VideoService) GetVideoStats(ctx context.Context, videoID string) (*model.VideoStats, error) {
	return s.videoRepo.GetStats(ctx, videoID)
}

func (s *VideoService) SyncVideoStats(ctx context.Context, videoID string) error {
	return s.videoRepo.SyncStatsToDB(ctx, videoID)
}

// ——— Hot / Feed ———

type HotVideosReq struct {
	TopN int64 `json:"top_n"`
}

func (s *VideoService) GetHotVideos(ctx context.Context, req *HotVideosReq) ([]*VideoDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	topN := int64(20)
	if req != nil {
		topN = req.TopN
	}
	if topN <= 0 || topN > 100 {
		topN = 20
	}

	videos, err := s.videoRepo.GetHotVideos(ctx, 0, topN-1)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		if errors.Is(err, repo.ErrVideoReadInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	videos, err = s.currentPublicVideos(ctx, videos, "", topN)
	if err != nil {
		return nil, err
	}
	details := s.batchDetails(ctx, videos)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	return details, nil
}

// ——— Search ———

type SearchReq struct {
	AuthorID string   `json:"author_id"`
	Keyword  string   `json:"keyword"`
	Tags     []string `json:"tags"`
	Page     int64    `json:"page"`
	PageSize int64    `json:"page_size"`
}

type SearchResp struct {
	List  []*VideoDetail `json:"list"`
	Total int64          `json:"total"`
	Page  int64          `json:"page"`
}

func (s *VideoService) SearchVideos(ctx context.Context, req *SearchReq) (*SearchResp, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if req == nil {
		return nil, ErrBadRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Page > 100 {
		req.Page = 100 // 深分页上限：防御 Mongo skip / ES from+size 放大
	}
	if req.PageSize <= 0 || req.PageSize > 50 {
		req.PageSize = 20
	}

	var videos []*model.Video
	var total int64
	var err error

	if req.AuthorID != "" {
		filter := repo.PublicVideoFilter()
		filter["author_id"] = req.AuthorID
		videos, total, err = s.videoRepo.List(ctx, filter, req.Page, req.PageSize)
	} else if len(req.Tags) > 0 {
		videos, total, err = s.videoRepo.SearchByTags(ctx, req.Tags, req.Page, req.PageSize)
	} else if req.Keyword != "" {
		videos, total, err = s.videoRepo.SearchByES(ctx, req.Keyword, req.Page, req.PageSize)
	} else {
		videos, total, err = s.videoRepo.List(ctx, repo.PublicVideoFilter(), req.Page, req.PageSize)
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		if (req.AuthorID != "" || len(req.Tags) > 0 || req.Keyword == "") && !errors.Is(err, repo.ErrVideoReadInvalid) {
			err = fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
		}
		return nil, fmt.Errorf("search videos: %w", err)
	}

	videos, err = s.currentPublicVideos(ctx, videos, req.AuthorID, req.PageSize)
	if err != nil {
		return nil, fmt.Errorf("search videos: %w", err)
	}
	details := s.batchDetails(ctx, videos)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	return &SearchResp{List: details, Total: total, Page: req.Page}, nil
}

// ——— List ———

func (s *VideoService) ListVideos(ctx context.Context, page, pageSize int64, status string) (*SearchResp, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if status != "" && status != "approved" {
		return nil, repo.ErrVideoReadInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	if page <= 0 {
		page = 1
	}
	if page > 100 {
		page = 100
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 20
	}

	lister, ok := s.videoRepo.(repo.PublicVideoLister)
	if !ok {
		return nil, repo.ErrVideoReadUnavailable
	}

	videos, total, err := lister.ListPublicVideos(ctx, page, pageSize)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, ctxErr)
	}
	if err != nil {
		if errors.Is(err, repo.ErrVideoReadInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	videos, err = s.currentPublicVideos(ctx, videos, "", pageSize)
	if err != nil {
		return nil, err
	}

	details := s.batchDetails(ctx, videos)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", repo.ErrVideoReadUnavailable, err)
	}
	return &SearchResp{List: details, Total: total, Page: page}, nil
}

// batchDetails 一次性批量取统计并组装详情（消除逐视频 GetStats 的 N+1）。
func (s *VideoService) batchDetails(ctx context.Context, videos []*model.Video) []*VideoDetail {
	return s.batchDetailsForUser(ctx, videos, "")
}

func (s *VideoService) batchDetailsForUser(ctx context.Context, videos []*model.Video, actor string) []*VideoDetail {
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		if v == nil {
			continue
		}
		ids = append(ids, v.ID.Hex())
	}
	statsMap := map[string]*model.VideoStats{}
	if len(ids) > 0 {
		if st, err := s.videoRepo.BatchGetStats(ctx, ids); err != nil {
			slog.Warn("batch get stats failed, use zero stats", "error", err)
		} else {
			statsMap = st
		}
	}
	details := make([]*VideoDetail, 0, len(videos))
	for _, v := range videos {
		if v == nil {
			continue
		}
		id := v.ID.Hex()
		stats := statsMap[id]
		if stats == nil {
			stats = &model.VideoStats{VideoID: id}
		}
		details = append(details, &VideoDetail{Video: v, Stats: stats, CollectCount: stats.CollectCount, ShareCount: stats.ShareCount})
	}
	s.fillVideoLikes(ctx, details, actor)
	return details
}

// ——— Review ———

func (s *VideoService) ReviewVideo(ctx context.Context, videoID, status string) error {
	switch status {
	case "approved", "rejected":
	default:
		return fmt.Errorf("invalid review status: %s", status)
	}
	return s.videoRepo.UpdateStatus(ctx, videoID, status)
}

// ——— Stats batch sync (cron) ———

type SyncStatsReq struct {
	VideoIDs []string `json:"video_ids"`
}

func (s *VideoService) BatchSyncStats(ctx context.Context, req *SyncStatsReq) (int, int, error) {
	success, failed := 0, 0
	for _, id := range req.VideoIDs {
		if err := s.videoRepo.SyncStatsToDB(ctx, id); err != nil {
			failed++
		} else {
			success++
		}
		// 避免瞬时压力
		time.Sleep(10 * time.Millisecond)
	}
	return success, failed, nil
}
