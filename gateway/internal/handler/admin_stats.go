package handler

import (
	"net/http"
	"strconv"
	"time"

	"feedvideo.local/shared-db/cache"
	config "feedvideo.local/shared-config"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"gorm.io/gorm"
)

type AdminStatsHandler struct {
	videoColl *mongo.Collection
	mysqlDB   *gorm.DB
	rdb       *cache.RedisClient
}

func NewAdminStatsHandler(videoColl *mongo.Collection, mysqlDB *gorm.DB, rdb *cache.RedisClient) *AdminStatsHandler {
	return &AdminStatsHandler{videoColl: videoColl, mysqlDB: mysqlDB, rdb: rdb}
}

// GetPlatformStats godoc
// @Summary 平台运营统计（管理员）
// @Description 聚合视频/用户/互动等平台级数据
// @Tags 管理后台
// @Produce json
// @Success 200 {object} map[string]interface{} "平台统计"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/admin/stats [get]
func (h *AdminStatsHandler) GetPlatformStats(c *gin.Context) {
	ctx := c.Request.Context()

	// 管理员校验：仅 admin_ids 白名单可访问
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

	// total_videos
	totalVideos, _ := h.videoColl.CountDocuments(ctx, bson.M{})

	// total_users
	var totalUsers int64
	h.mysqlDB.Raw("SELECT COUNT(*) FROM t_users").Scan(&totalUsers)

	// today_plays/uploads
	today := time.Now().Format("20060102")
	todayPlays, _ := h.rdb.Get(ctx, "stats:daily:plays:"+today)
	todayUploads, _ := h.rdb.Get(ctx, "stats:daily:uploads:"+today)

	plays, _ := strconv.ParseInt(todayPlays, 10, 64)
	uploads, _ := strconv.ParseInt(todayUploads, 10, 64)

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"total_videos":  totalVideos,
		"total_users":   totalUsers,
		"today_plays":   plays,
		"today_uploads": uploads,
	}})
}
