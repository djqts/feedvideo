package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"feedvideo.local/video/repo"
	"feedvideo.local/video/service"
)

// CommentHandler 处理评论相关 HTTP 请求
type CommentHandler struct {
	commentSvc *service.CommentService
}

// NewCommentHandler 创建 CommentHandler
func NewCommentHandler(commentSvc *service.CommentService) *CommentHandler {
	return &CommentHandler{commentSvc: commentSvc}
}

func writeCommentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrCommentInvalid), errors.Is(err, service.ErrBadRequest):
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid comment request"})
	case errors.Is(err, repo.ErrCommentNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "comment target not found"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "comments temporarily unavailable"})
	}
}

// PostComment godoc
// @Summary 发表评论/回复
// @Tags 评论
// @Accept json
// @Produce json
// @Param id path string true "视频 ID"
// @Param body body object true "content + 可选 parent_id"
// @Success 201 {object} map[string]interface{} "评论创建成功 {comment_id}"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/videos/{id}/comments [post]
func (h *CommentHandler) PostComment(c *gin.Context) {
	videoID := c.Param("id")
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	var body struct {
		Content  string `json:"content" binding:"required"`
		ParentID string `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	req := &service.PostCommentReq{
		VideoID:  videoID,
		UserID:   userID,
		Content:  body.Content,
		ParentID: body.ParentID,
	}
	resp, err := h.commentSvc.PostComment(c.Request.Context(), req)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "ok", "data": resp})
}

// GetComments godoc
// @Summary 获取评论列表
// @Tags 评论
// @Produce json
// @Param id path string true "视频 ID"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Success 200 {object} map[string]interface{} "评论列表"
// @Router /api/v1/videos/{id}/comments [get]
func (h *CommentHandler) GetComments(c *gin.Context) {
	videoID := c.Param("id")
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "20"), 10, 64)
	currentUserID := getOptionalUserID(c)
	req := &service.CommentListReq{
		VideoID:  videoID,
		Page:     page,
		PageSize: pageSize,
	}
	resp, err := h.commentSvc.GetComments(c.Request.Context(), req, currentUserID)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}

// LikeComment godoc
// @Summary 点赞评论
// @Tags 评论
// @Produce json
// @Param id path string true "评论 ID"
// @Success 200 {object} map[string]string "点赞成功"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/comments/{id}/like [post]
func (h *CommentHandler) LikeComment(c *gin.Context) {
	commentID := c.Param("id")
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	req := &service.CommentLikeReq{
		CommentID: commentID,
		UserID:    userID,
	}
	if err := h.commentSvc.LikeComment(c.Request.Context(), req); err != nil {
		writeLikeError(c, err, h.commentSvc.DurableLikesEnabled())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// UnlikeComment godoc
// @Summary 取消评论点赞
// @Tags 评论
// @Produce json
// @Param id path string true "评论 ID"
// @Success 200 {object} map[string]string "取消成功"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/comments/{id}/like [delete]
func (h *CommentHandler) UnlikeComment(c *gin.Context) {
	commentID := c.Param("id")
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	req := &service.CommentLikeReq{
		CommentID: commentID,
		UserID:    userID,
	}
	if err := h.commentSvc.UnlikeComment(c.Request.Context(), req); err != nil {
		writeLikeError(c, err, h.commentSvc.DurableLikesEnabled())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// ReportComment godoc
// @Summary 举报评论
// @Description 举报写入 ES reports 索引，供管理后台处理
// @Tags 评论
// @Accept json
// @Produce json
// @Param id path string true "评论 ID"
// @Param body body object true "reason: 举报原因"
// @Success 200 {object} map[string]string "举报成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/comments/{id}/report [post]
func (h *CommentHandler) ReportComment(c *gin.Context) {
	commentID := c.Param("id")
	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: " + err.Error()})
		return
	}
	req := &service.ReportCommentReq{
		CommentID: commentID,
		Reason:    body.Reason,
	}
	if err := h.commentSvc.ReportComment(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

// DeleteComment godoc
// @Summary 删除评论
// @Tags 评论
// @Produce json
// @Param id path string true "评论 ID"
// @Success 200 {object} map[string]string "删除成功"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/comments/{id} [delete]
func (h *CommentHandler) DeleteComment(c *gin.Context) {
	commentID := c.Param("id")
	userID, ok := likeActor(c, true)
	if !ok {
		return
	}
	if err := h.commentSvc.DeleteComment(c.Request.Context(), commentID, userID); err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "forbidden"})
			return
		}
		writeCommentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}
