package handler

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
)

// behaviorStatsRepo is the minimal repository dependency of BehaviorStatsHandler.
// *userrepo.UserBehavior satisfies it.
type behaviorStatsRepo interface {
	AggregateStats(ctx context.Context, userID int64, days int) (map[string]int64, error)
}

type BehaviorStatsHandler struct {
	repo      behaviorStatsRepo
	adminIDs  []string
}

func NewBehaviorStatsHandler(repo behaviorStatsRepo) *BehaviorStatsHandler {
	return &BehaviorStatsHandler{repo: repo}
}

// WithAdminIDs 注入管理员白名单（用于平台范围行为统计鉴权）。
func (h *BehaviorStatsHandler) WithAdminIDs(ids []string) *BehaviorStatsHandler {
	h.adminIDs = ids
	return h
}

// GetStats godoc
// @Summary 用户行为统计
// @Description 聚合 play/like/collect/share/follow 行为次数（1-30 天）
// @Tags 统计
// @Produce json
// @Param days query int false "统计天数(1-30)" default(7)
// @Success 200 {object} map[string]interface{} "行为统计 {actions}"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/behavior/stats [get]
func (h *BehaviorStatsHandler) GetStats(c *gin.Context) {
	uid, ok := c.Get("userID")
	if !ok {
		c.JSON(401, gin.H{"code": 401, "message": "not authenticated"})
		return
	}
	userID, _ := strconv.ParseInt(uid.(string), 10, 64)
	if userID == 0 {
		c.JSON(401, gin.H{"code": 401, "message": "not authenticated"})
		return
	}
	// 平台范围（scope=platform）仅管理员可用，聚合全平台行为（userID=0）
	scope := c.DefaultQuery("scope", "")
	if scope == "platform" {
		isAdmin := false
		for _, id := range h.adminIDs {
			if id == uid.(string) {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			c.JSON(403, gin.H{"code": 403, "message": "forbidden"})
			return
		}
		userID = 0
	}

	daysStr := c.DefaultQuery("days", "7")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 || days > 30 {
		msg := "days must be 1-30"
		if err != nil {
			msg = "invalid days parameter"
		}
		c.JSON(400, gin.H{"code": 400, "message": msg})
		return
	}

	stats, err := h.repo.AggregateStats(c.Request.Context(), userID, days)
	if err != nil {
		c.JSON(500, gin.H{"code": 500, "message": "internal server error"})
		return
	}

	// Zero-fill all expected keys
	result := map[string]int64{"play": 0, "like": 0, "collect": 0, "share": 0, "follow": 0}
	for k, v := range stats {
		result[k] = v
	}
	c.JSON(200, gin.H{"code": 0, "data": gin.H{"actions": result}})
}
