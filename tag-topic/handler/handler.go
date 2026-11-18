package handler

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"feedvideo.local/tag-topic/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Handler struct {
	svc *service.TagTopicService
}

func NewHandler(svc *service.TagTopicService) *Handler { return &Handler{svc: svc} }

// CreateTopic godoc
// @Summary 创建话题
// @Tags 话题
// @Accept json
// @Produce json
// @Param body body object true "name + description"
// @Success 201 {object} map[string]interface{} "话题创建成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/topics [post]
func (h *Handler) CreateTopic(c *gin.Context) {
	var body struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	if utf8.RuneCountInString(body.Name) > 30 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "topic name too long (max 30)"})
		return
	}
	if utf8.RuneCountInString(body.Description) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "topic description too long (max 500)"})
		return
	}
	t, err := h.svc.Create(c.Request.Context(), body.Name, body.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "ok", "data": t})
}

// GetTopicByID godoc
// @Summary 按 ID 获取话题详情
// @Tags 话题
// @Produce json
// @Param topicId path string true "话题 ID"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "话题详情 + 视频列表"
// @Failure 404 {object} map[string]string "不存在"
// @Router /api/v1/topics/id/{topicId} [get]
func (h *Handler) GetTopicByID(c *gin.Context) {
	topicID := c.Param("topicId")
	if _, err := primitive.ObjectIDFromHex(topicID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid topic id"})
		return
	}
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	detail, err := h.svc.GetDetailByID(c.Request.Context(), topicID, page, size)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "topic temporarily unavailable"})
		return
	}
	if detail == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": detail})
}

// GetTopic godoc
// @Summary 按名称获取话题详情
// @Tags 话题
// @Produce json
// @Param name path string true "话题名称"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "话题详情 + 视频列表"
// @Failure 404 {object} map[string]string "不存在"
// @Router /api/v1/topics/{name} [get]
func (h *Handler) GetTopic(c *gin.Context) {
	name := c.Param("name")
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	detail, err := h.svc.GetDetail(c.Request.Context(), name, page, size)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "topic temporarily unavailable"})
		return
	}
	if detail == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": detail})
}

// HotTopics godoc
// @Summary 热门话题
// @Tags 话题
// @Produce json
// @Param top_n query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "热门话题列表"
// @Router /api/v1/topics/hot [get]
func (h *Handler) HotTopics(c *gin.Context) {
	n, _ := strconv.ParseInt(c.DefaultQuery("top_n", "20"), 10, 64)
	topics, err := h.svc.HotTopics(c.Request.Context(), n)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": topics})
}
