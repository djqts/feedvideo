package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	config "feedvideo.local/shared-config"
	"feedvideo.local/shared-db/cache"
	userModel "feedvideo.local/user/model"
	userrepo "feedvideo.local/user/repo"

	"github.com/gin-gonic/gin"

	"feedvideo.local/video/model"
	"feedvideo.local/video/orphan"
	"feedvideo.local/video/repo"
	"feedvideo.local/video/service"
)

// VideoHandler 处理视频相关 HTTP 请求
type VideoHandler struct {
	videoSvc     *service.VideoService
	rdb          *cache.RedisClient
	behaviorRepo *userrepo.UserBehavior
	tracker      *orphan.Tracker
}

// NewVideoHandler 创建 VideoHandler
func NewVideoHandler(videoSvc *service.VideoService, rdb *cache.RedisClient, behaviorRepo *userrepo.UserBehavior, tracker ...*orphan.Tracker) *VideoHandler {
	h := &VideoHandler{videoSvc: videoSvc, rdb: rdb, behaviorRepo: behaviorRepo}
	if len(tracker) > 0 {
		h.tracker = tracker[0]
	}
	return h
}

func (h *VideoHandler) logBehavior(userID int64, action, targetID, targetType string) {
	if userID == 0 || h.behaviorRepo == nil {
		return
	}
	go func() {
		defer func() { recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		log := &userModel.UserBehaviorLog{
			UserID:     userID,
			Action:     action,
			TargetID:   targetID,
			TargetType: targetType,
			Timestamp:  time.Now(),
		}
		if err := h.behaviorRepo.InsertLog(ctx, log); err != nil {
			// slog.Warn("behavior log failed", "action", action, "error", err)
		}
	}()
}

func (h *VideoHandler) incrDaily(ctx *gin.Context, key string) {
	if h.rdb == nil {
		return
	}
	today := time.Now().Format("20060102")
	fullKey := fmt.Sprintf("stats:daily:%s:%s", key, today)
	v, _ := h.rdb.Get(ctx.Request.Context(), fullKey)
	n, _ := strconv.ParseInt(v, 10, 64)
	n++
	_ = h.rdb.Set(ctx.Request.Context(), fullKey, strconv.FormatInt(n, 10), 2592000*time.Second)
}

// getOptionalUserID 从 JWT context 提取 userID，不存在返回空字符串
func getOptionalUserID(c *gin.Context) string {
	v, ok := c.Get("userID")
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// Publish godoc
// @Summary 发布视频
// @Description 创建视频记录（status=pending，待审核），需先通过 /api/v1/upload 获取 video_url
// @Tags 视频
// @Accept json
// @Produce json
// @Param body body service.PublishReq true "发布信息"
// @Success 201 {object} map[string]interface{} "发布成功 {video_id}"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/videos [post]
func (h *VideoHandler) Publish(c *gin.Context) {
	var req service.PublishReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	req.AuthorID = getOptionalUserID(c) // JWT sub 写入作者
	if !service.ValidPublishActor(req.AuthorID) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	var keyValues []string
	for name, values := range c.Request.Header {
		if strings.EqualFold(name, "Idempotency-Key") {
			keyValues = append(keyValues, values...)
			if len(values) == 0 {
				keyValues = append(keyValues, "")
			}
		}
	}
	if len(keyValues) > 0 {
		if len(keyValues) != 1 || !service.ValidPublishKey(keyValues[0]) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
			return
		}
		req.PublishKey = keyValues[0]
	}
	if req.UploadID != "" && req.PublishKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	resp, err := h.videoSvc.Publish(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, repo.ErrPublishConflict) || errors.Is(err, repo.ErrUploadConflict) {
			c.JSON(http.StatusConflict, gin.H{"code": 409, "message": "publish conflict"})
			return
		}
		if errors.Is(err, repo.ErrUploadExpired) {
			c.JSON(http.StatusGone, gin.H{"code": 410, "message": "upload expired"})
			return
		}
		if errors.Is(err, repo.ErrUploadNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "upload not found"})
			return
		}
		if errors.Is(err, service.ErrMediaUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "media storage unavailable"})
			return
		}
		if errors.Is(err, service.ErrBadRequest) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "ok", "data": resp})
	if resp.Replayed {
		return
	}
	if h.tracker != nil {
		// Claim uploaded objects now referenced by a published video.
		for _, u := range []string{req.VideoURL, req.CoverURL} {
			if u == "" {
				continue
			}
			if b, o, ok := orphan.ParseStorageURL(u); ok {
				if err := orphan.ClaimWithRetry(c.Request.Context(), h.tracker.Claim, b, o); err != nil {
					// 认领失败：对象暂留 pending，孤儿回收器会通过 referenced 对账跳过已发布对象
					log.Printf("[publish] claim orphan object failed (reconciled by reaper): bucket=%s object=%s err=%v", b, o, err)
				}
			}
		}
	}
	if h.rdb != nil {
		if resp.JobID != "" {
			data, _ := json.Marshal(map[string]string{"video_id": resp.VideoID, "job_id": resp.JobID})
			if err := h.rdb.LPush(c.Request.Context(), "media:wakeup", string(data)); err != nil {
				log.Printf("media wakeup hint failed: %v", err)
			}
		} else {
			// 发布后入队媒体处理任务（转码 + 无封面时缩略图），由 media-worker 处理。
			if b, o, ok := orphan.ParseStorageURL(req.VideoURL); ok {
				if data, err := json.Marshal(map[string]any{
					"video_id": resp.VideoID, "bucket": b, "object": o,
					"cover": req.CoverURL != "", "enqueued_at": time.Now().Unix(),
				}); err == nil {
					if err := h.rdb.LPush(c.Request.Context(), "media:tasks", string(data)); err != nil {
						log.Printf("enqueue media task failed: %v", err)
					} else {
						// P3.1：状态计数 queued（与 media-worker 的 processing/succeeded/failed 对应）
						_, _ = h.rdb.Incr(c.Request.Context(), "metrics:video:status:queued")
						// 任务年龄：ZSet score=入队时间（media-worker 完成时 ZRem，供 oldest age 指标）
						_ = h.rdb.ZAdd(c.Request.Context(), "media:task_ts", float64(time.Now().Unix()), resp.VideoID)
					}
				}
			}
		}
	}
	h.incrDaily(c, "uploads")
}

// GetVideo godoc
// @Summary 获取视频详情
// @Description 返回视频信息与统计，可选认证（登录时附带点赞/收藏状态并记录播放）
// @Tags 视频
// @Produce json
// @Param id path string true "视频 ID"
// @Success 200 {object} map[string]interface{} "视频详情"
// @Failure 500 {object} map[string]string "服务器错误"
// @Router /api/v1/videos/{id} [get]
func (h *VideoHandler) GetVideo(c *gin.Context) {
	detailCtx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	asyncOwnsCancel := false
	defer func() {
		if !asyncOwnsCancel {
			cancel()
		}
	}()
	videoID := c.Param("id")
	if len(videoID) != 24 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid video id"})
		return
	}
	currentUserID := getOptionalUserID(c)
	detail, err := h.videoSvc.GetVideo(detailCtx, videoID, currentUserID)
	if err != nil {
		if errors.Is(err, repo.ErrVideoReadInvalid) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid video id"})
			return
		}
		if errors.Is(err, service.ErrVideoPendingReview) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "video pending review", "details": gin.H{"status": "pending"}})
			return
		}
		if errors.Is(err, service.ErrVideoRejected) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "video rejected", "details": gin.H{"status": "rejected"}})
			return
		}
		if errors.Is(err, repo.ErrVideoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "video not found"})
			return
		}
		if errors.Is(err, repo.ErrVideoReadUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "video temporarily unavailable"})
			return
		}
		if errors.Is(err, repo.ErrLikeUnavailable) || errors.Is(err, repo.ErrLikeInvalid) || errors.Is(err, service.ErrLikeTargetNotFound) {
			writeLikeError(c, err, true)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": detail})
	if !model.IsPubliclyVisible(detail.Video) || detailCtx.Err() != nil {
		return
	}
	h.incrDetailDaily(detailCtx)
	// Async play log
	if currentUserID != "" {
		uid, _ := strconv.ParseInt(currentUserID, 10, 64)
		asyncOwnsCancel = h.logDetailPlay(detailCtx, cancel, uid, detail.ID.Hex())
	}
}

// Detail effects retain the same entry budget; other endpoints keep their helpers.
func (h *VideoHandler) incrDetailDaily(ctx context.Context) {
	if h.rdb == nil || ctx.Err() != nil {
		return
	}
	key := "stats:daily:plays:" + time.Now().Format("20060102")
	v, _ := h.rdb.Get(ctx, key)
	if ctx.Err() != nil {
		return
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	_ = h.rdb.Set(ctx, key, strconv.FormatInt(n+1, 10), 2592000*time.Second)
}

// The asynchronous write owns cancellation only when scheduled. Its parent
// cancellation and absolute deadline remain intact after the service returns.
func (h *VideoHandler) logDetailPlay(ctx context.Context, cancel context.CancelFunc, userID int64, videoID string) bool {
	if userID <= 0 || h.behaviorRepo == nil || ctx.Err() != nil {
		return false
	}
	go func() {
		defer cancel()
		defer func() { recover() }()
		if ctx.Err() != nil {
			return
		}
		_ = h.behaviorRepo.InsertLog(ctx, &userModel.UserBehaviorLog{
			UserID: userID, Action: "play", TargetID: videoID, TargetType: "video", Timestamp: time.Now(),
		})
	}()
	return true
}

// UpdateVideo godoc
// @Summary 更新视频信息
// @Tags 视频
// @Accept json
// @Produce json
// @Param id path string true "视频 ID"
// @Param body body service.UpdateVideoReq true "更新字段"
// @Success 200 {object} map[string]string "更新成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/videos/{id} [put]
func (h *VideoHandler) UpdateVideo(c *gin.Context) {
	videoID := c.Param("id")
	var req service.UpdateVideoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	userID := getOptionalUserID(c)
	if err := h.videoSvc.UpdateVideo(c.Request.Context(), videoID, &req, userID); err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// DeleteVideo godoc
// @Summary 删除视频（软删除）
// @Tags 视频
// @Produce json
// @Param id path string true "视频 ID"
// @Success 200 {object} map[string]string "删除成功"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/videos/{id} [delete]
func (h *VideoHandler) DeleteVideo(c *gin.Context) {
	videoID := c.Param("id")
	userID := getOptionalUserID(c)
	if err := h.videoSvc.DeleteVideo(c.Request.Context(), videoID, userID); err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// GetHotVideos godoc
// @Summary 获取热门视频
// @Tags 视频
// @Produce json
// @Param top_n query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "热门视频列表"
// @Router /api/v1/videos/hot [get]
func (h *VideoHandler) GetHotVideos(c *gin.Context) {
	topN, ok := publicListQueryInt(c, "top_n", 20, 100)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	req := &service.HotVideosReq{TopN: topN}
	videos, err := h.videoSvc.GetHotVideos(ctx, req)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if publicListError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": videos})
}

// SearchVideos godoc
// @Summary 搜索视频
// @Tags 视频
// @Produce json
// @Param author_id query string false "作者用户 ID"
// @Param keyword query string false "关键词"
// @Param tags query []string false "标签"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "搜索结果"
// @Router /api/v1/videos/search [get]
func (h *VideoHandler) SearchVideos(c *gin.Context) {
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "20"), 10, 64)
	if page <= 0 || page > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "page", "reason": "must be between 1 and 100"}})
		return
	}
	keyword := c.Query("keyword")
	tags := c.QueryArray("tags")
	req := &service.SearchReq{
		AuthorID: c.Query("author_id"),
		Keyword:  keyword,
		Tags:     tags,
		Page:     page,
		PageSize: pageSize,
	}
	resp, err := h.videoSvc.SearchVideos(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, repo.ErrSearchUnavailable) || errors.Is(err, repo.ErrVideoReadUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "搜索暂时不可用，请稍后重试"})
			return
		}
		if errors.Is(err, repo.ErrVideoReadInvalid) || errors.Is(err, service.ErrBadRequest) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "搜索失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}

// ListVideos godoc
// @Summary 视频列表
// @Tags 视频
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param status query string false "状态过滤 approved（或不传）"
// @Success 200 {object} map[string]interface{} "视频列表"
// @Router /api/v1/videos [get]
func (h *VideoHandler) ListVideos(c *gin.Context) {
	page, ok := publicListQueryInt(c, "page", 1, 100)
	if !ok {
		return
	}
	pageSize, ok := publicListQueryInt(c, "page_size", 20, 50)
	if !ok {
		return
	}
	status := c.Query("status")
	switch status {
	case "", "approved":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "status", "reason": "must be approved or empty"}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	resp, err := h.videoSvc.ListVideos(ctx, page, pageSize, status)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if publicListError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}

func publicListQueryInt(c *gin.Context, name string, fallback, maximum int64) (int64, bool) {
	values, exists := c.Request.URL.Query()[name]
	if !exists {
		return fallback, true
	}
	if len(values) == 1 {
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err == nil && value >= 1 && value <= maximum {
			return value, true
		}
	}
	c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": name, "reason": fmt.Sprintf("must be between 1 and %d", maximum)}})
	return 0, false
}

func publicListError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, repo.ErrVideoReadUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "video source is temporarily unavailable"})
	case errors.Is(err, repo.ErrVideoReadInvalid), errors.Is(err, service.ErrBadRequest):
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal server error"})
	}
	return true
}

// ReviewVideo godoc
// @Summary 视频审核
// @Description 将视频状态流转为 approved / rejected
// @Tags 视频
// @Accept json
// @Produce json
// @Param id path string true "视频 ID"
// @Param body body object true "status: approved/rejected"
// @Success 200 {object} map[string]string "审核成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/videos/{id}/review [put]
func (h *VideoHandler) ReviewVideo(c *gin.Context) {
	videoID := c.Param("id")
	// 审核仅管理员可执行
	uidStr, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
		return
	}
	isAdmin := false
	for _, id := range config.AppConfig.AdminIDs {
		if id == uidStr.(string) {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	if err := h.videoSvc.ReviewVideo(c.Request.Context(), videoID, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// BatchSyncStats godoc
// @Summary 批量同步视频统计（Redis → Mongo）
// @Tags 视频
// @Accept json
// @Produce json
// @Param body body service.SyncStatsReq true "视频 ID 列表"
// @Success 200 {object} map[string]interface{} "同步结果"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/videos/sync-stats [post]
func (h *VideoHandler) BatchSyncStats(c *gin.Context) {
	var req service.SyncStatsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	if len(req.VideoIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "video ids required"})
		return
	}
	if len(req.VideoIDs) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "max 100 video ids per batch"})
		return
	}
	success, failed, err := h.videoSvc.BatchSyncStats(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{
		"success": success,
		"failed":  failed,
	}})
}
