package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminRestoreComment godoc
// @Summary 恢复被举报隐藏的评论（管理员）
// @Description 将 hidden 评论恢复为 active，并原子补回一次 comment_count
// @Tags 审核
// @Produce json
// @Param id path string true "评论 ID"
// @Success 200 {object} map[string]interface{} "restored"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Failure 403 {object} map[string]string "非管理员"
// @Failure 503 {object} map[string]string "存储不可用"
// @Router /api/v1/admin/comments/{id}/restore [put]
func (h *CommentHandler) AdminRestoreComment(c *gin.Context) {
	restored, err := h.commentSvc.RestoreComment(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeCommentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"restored": restored}})
}
