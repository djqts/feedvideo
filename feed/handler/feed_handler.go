package handler

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	feedmodel "feedvideo.local/feed/model"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
	videoservice "feedvideo.local/video/service"
)

// feedService is the interface FeedHandler needs from the feed service layer.
type feedService interface {
	Hot(ctx context.Context, size int64) (*feedmodel.FeedResp, error)
	Following(ctx context.Context, uid string, size int64) (*feedmodel.FeedResp, error)
	Nearby(ctx context.Context, lng, lat, radius float64, size int64) (*feedmodel.FeedResp, error)
	Recommend(ctx context.Context, uid string, size int64) (*feedmodel.FeedResp, error)
}

type FeedHandler struct {
	svc      feedService
	videoSvc videoDetailsService
}

type videoDetailsService interface {
	ListDetails(ctx context.Context, videos []*videomodel.Video, userID string) []*videoservice.VideoDetail
}

func NewFeedHandler(svc feedService) *FeedHandler {
	return &FeedHandler{svc: svc}
}

func (h *FeedHandler) SetVideoService(svc videoDetailsService) { h.videoSvc = svc }

// getOptionalUserID 从 JWT context 提取 userID（OptionalAuth 设置 claims["sub"]=string）
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

// GetFeed godoc
// @Summary 获取 Feed 信息流
// @Description 支持 recommend(推荐) / following(关注) / nearby(同城) / hot(热门)，可选认证
// @Tags Feed
// @Produce json
// @Param type query string false "类型: recommend/following/nearby/hot" default(hot)
// @Param size query int false "数量(1-50)" default(10)
// @Param lng query number false "经度(nearby)"
// @Param lat query number false "纬度(nearby)"
// @Param radius query number false "半径米(nearby)" default(5000)
// @Success 200 {object} map[string]interface{} "Feed 列表 {list, has_more}"
// @Failure 500 {object} map[string]string "服务器错误"
// @Failure 503 {object} map[string]string "视频来源暂时不可用"
// @Router /api/v1/feed [get]
func (h *FeedHandler) GetFeed(c *gin.Context) {
	ftype := feedmodel.FeedType(strings.ToLower(c.Query("type"))) // 大小写容错
	uid := getOptionalUserID(c)
	sizeRaw := "10"
	if c.Request.URL.Query().Has("size") {
		sizeRaw = c.Query("size")
	}
	size, sizeErr := strconv.ParseInt(sizeRaw, 10, 64)
	if sizeErr != nil || size <= 0 || size > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "size", "reason": "must be 1-50"}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		writeFeedError(c, err)
		return
	}

	var (
		resp *feedmodel.FeedResp
		err  error
	)
	switch ftype {
	case feedmodel.FeedTypeRecommend:
		resp, err = h.svc.Recommend(ctx, uid, size)
	case feedmodel.FeedTypeFollowing:
		resp, err = h.svc.Following(ctx, uid, size)
	case feedmodel.FeedTypeNearby:
		lngRaw := c.Query("lng")
		latRaw := c.Query("lat")
		if lngRaw == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "lng", "reason": "required for nearby feed"}})
			return
		}
		if latRaw == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "lat", "reason": "required for nearby feed"}})
			return
		}
		lng, errLng := strconv.ParseFloat(lngRaw, 64)
		if errLng != nil || !finiteNumber(lng) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "lng", "reason": "must be a number"}})
			return
		}
		lat, errLat := strconv.ParseFloat(latRaw, 64)
		if errLat != nil || !finiteNumber(lat) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "lat", "reason": "must be a number"}})
			return
		}
		if lng < -180 || lng > 180 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "lng", "reason": "must be between -180 and 180"}})
			return
		}
		if lat < -90 || lat > 90 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "lat", "reason": "must be between -90 and 90"}})
			return
		}
		radiusRaw := "5000"
		if c.Request.URL.Query().Has("radius") {
			radiusRaw = c.Query("radius")
		}
		radius, errRad := strconv.ParseFloat(radiusRaw, 64)
		if errRad != nil || !finiteNumber(radius) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "radius", "reason": "must be a number"}})
			return
		}
		if radius < 1 || radius > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "radius", "reason": "must be between 1 and 100000 meters"}})
			return
		}
		resp, err = h.svc.Nearby(ctx, lng, lat, radius, size)
	case feedmodel.FeedTypeHot:
		resp, err = h.svc.Hot(ctx, size)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid feed type", "details": gin.H{"parameter": "type", "reason": "unsupported value"}})
		return
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		writeFeedError(c, ctxErr)
		return
	}
	if err != nil {
		writeFeedError(c, err)
		return
	}
	if h.videoSvc != nil && resp != nil {
		videos := make([]*videomodel.Video, 0, len(resp.List))
		for _, item := range resp.List {
			if item != nil && item.Video != nil {
				videos = append(videos, item.Video)
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			writeFeedError(c, ctxErr)
			return
		}
		details := h.videoSvc.ListDetails(ctx, videos, uid)
		if ctxErr := ctx.Err(); ctxErr != nil {
			writeFeedError(c, ctxErr)
			return
		}
		// Feed candidates may be shared in a degraded cache; never write user
		// interaction flags into the cached response or its existing items.
		list := make([]*feedmodel.FeedItem, 0, len(details))
		for _, detail := range details {
			list = append(list, &feedmodel.FeedItem{
				Video: detail.Video, Stats: detail.Stats, IsLiked: detail.IsLiked, LikeStateKnown: detail.LikeStateKnown,
				IsCollected: detail.IsCollected, CollectCount: detail.CollectCount, ShareCount: detail.ShareCount,
			})
		}
		resp = &feedmodel.FeedResp{List: list, HasMore: resp.HasMore}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		writeFeedError(c, ctxErr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}

func finiteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func writeFeedError(c *gin.Context, err error) {
	if errors.Is(err, videorepo.ErrVideoReadUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "video source is temporarily unavailable"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal server error"})
}
