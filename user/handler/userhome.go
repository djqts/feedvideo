package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"feedvideo.local/shared-auth/middlewares"
	config "feedvideo.local/shared-config"
	"feedvideo.local/user/model"
	"feedvideo.local/user/repo"
	"feedvideo.local/user/service"
)

// setAuthCookie sets an auth cookie with SameSite=Lax and conditional Secure
// based on the X-Forwarded-Proto header (set by production nginx/reverse proxy).
func setAuthCookie(c *gin.Context, name, value string, maxAge int) {
	secure := strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, value, maxAge, "/", "", secure, true)
}

type UserHomeHandler struct {
	userService   *service.UserService
	userRelation  *service.RelationService
	searchService *service.SearchService
	behaviorRepo  *repo.UserBehavior
	watchHandler  *WatchHandler
}

func (h *UserHomeHandler) WithWatchHandler(watch *WatchHandler) *UserHomeHandler {
	h.watchHandler = watch
	return h
}
func (h *UserHomeHandler) IngestWatch(c *gin.Context) {
	if h == nil || h.watchHandler == nil {
		c.JSON(503, gin.H{"code": "STORAGE_UNAVAILABLE", "message": "STORAGE_UNAVAILABLE", "request_id": c.GetString("request_id")})
		return
	}
	h.watchHandler.Ingest(c)
}

func (h *UserHomeHandler) WatchHistory(c *gin.Context) {
	if h == nil || h.watchHandler == nil {
		watchReadReply(c, 503, "STORAGE_UNAVAILABLE")
		return
	}
	h.watchHandler.History(c)
}
func (h *UserHomeHandler) WatchSession(c *gin.Context) {
	if h == nil || h.watchHandler == nil {
		watchReadReply(c, 503, "STORAGE_UNAVAILABLE")
		return
	}
	h.watchHandler.Session(c)
}

// IsUserActive 供 RequireActiveAccount 中间件使用：校验账号状态（30s 缓存）。
// DB 异常 fail-open（返回 true），避免基础设施故障放大为全体下线。
func (h *UserHomeHandler) IsUserActive(ctx context.Context, uid string) bool {
	uidInt, err := strconv.ParseInt(uid, 10, 64)
	if err != nil {
		return false
	}
	active, err := h.userService.IsUserActive(ctx, uidInt)
	if err != nil {
		return true
	}
	return active
}

// IsNotMuted 供 RequireNotMuted 中间件使用：禁言返回 false（DB 异常 fail-open 放行）。
func (h *UserHomeHandler) IsNotMuted(ctx context.Context, uid string) bool {
	uidInt, err := strconv.ParseInt(uid, 10, 64)
	if err != nil {
		return false
	}
	muted, err := h.userService.IsUserMuted(ctx, uidInt)
	if err != nil {
		return true
	}
	return !muted
}

func NewUserHomeHandler(userService *service.UserService, userRelation *service.RelationService, searchService *service.SearchService, behaviorRepo *repo.UserBehavior) *UserHomeHandler {
	return &UserHomeHandler{
		userService:   userService,
		userRelation:  userRelation,
		searchService: searchService,
		behaviorRepo:  behaviorRepo,
	}
}

// Register godoc
// @Summary 用户注册
// @Description 使用用户名和密码注册新账号
// @Tags 用户
// @Accept json
// @Produce json
// @Param body body model.UserRegisterRequest true "注册信息"
// @Success 201 {object} map[string]string "注册成功"
// @Failure 400 {object} map[string]string "用户名已存在或参数错误"
// @Failure 500 {object} map[string]string "服务器错误"
// @Router /register [post]
func (h *UserHomeHandler) Register(c *gin.Context) {
	var req model.UserRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.userService.Register(c.Request.Context(), &req); err != nil {
		if errors.Is(err, service.ErrUserAlreadyExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "User registered successfully"})
}

// Login godoc
// @Summary 用户登录
// @Description 登录成功后通过 httpOnly Cookie 下发 access_token 与 refresh_token
// @Tags 用户
// @Accept json
// @Produce json
// @Param body body model.UserLoginRequest true "登录信息"
// @Success 200 {object} map[string]string "登录成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "用户名或密码错误"
// @Router /login [post]
func (h *UserHomeHandler) Login(c *gin.Context) {
	var req model.UserLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	tokenPair, err := h.userService.Login(c.Request.Context(), &req)
	if err != nil {
		var infra *service.InfraError
		if errors.As(err, &infra) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service unavailable, please retry"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	setAuthCookie(c, "access_token", tokenPair.AccessToken, int(tokenPair.ExpiresIn))
	refreshMaxAge := 7 * 24 * 60 * 60 // 7天
	setAuthCookie(c, "refresh_token", tokenPair.RefreshToken, refreshMaxAge)
	// P2.1：登录时下发 CSRF 双提交 token（非 HttpOnly，前端 JS 读取后随写请求回传）
	middlewares.SetCSRFCookie(c, middlewares.GenerateCSRFToken())
	c.JSON(http.StatusOK, gin.H{"message": "Login successful"})
}

// Logout godoc
// @Summary 用户退出登录
// @Description 清除服务端 refresh token 记录并清理 Cookie
// @Tags 用户
// @Produce json
// @Success 200 {object} map[string]string "退出成功"
// @Router /logout [post]
func (h *UserHomeHandler) Logout(c *gin.Context) {

	refreshToken, _ := c.Cookie("refresh_token")
	if refreshToken != "" {
		userIDStr := c.GetString("userID")
		var req struct {
			UserID string `json:"user_id"`
		}
		if c.Request.Body != nil && c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
				return
			}
		}
		// The hint is only a lookup key for the refresh capability. A retry from
		// an earlier account must never revoke or clear a newly logged-in account.
		if userIDStr != "" && req.UserID != "" && req.UserID != userIDStr {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid identity"})
			return
		}
		if userIDStr == "" {
			userIDStr = req.UserID
		}
		uid, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != userIDStr {
			status := http.StatusBadRequest
			if userIDStr == "" {
				status = http.StatusUnauthorized
			}
			c.JSON(status, gin.H{"error": "invalid identity"})
			return
		}
		if err := h.userService.Logout(c.Request.Context(), userIDStr, refreshToken); err != nil {
			if errors.Is(err, repo.ErrInvalidRefreshToken) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
			} else {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service unavailable, please retry"})
			}
			return
		}
	}
	setAuthCookie(c, "access_token", "", -1)
	setAuthCookie(c, "refresh_token", "", -1)
	setAuthCookie(c, middlewares.CSRFCookieName, "", -1)
	c.JSON(http.StatusOK, gin.H{"message": "Logout successful"})
}

// RefreshToken godoc
// @Summary 刷新访问令牌
// @Description 使用 refresh_token Cookie 换取新的 access_token / refresh_token
// @Tags 用户
// @Accept json
// @Produce json
// @Param body body object false "可选 user_id"
// @Success 200 {object} map[string]string "刷新成功"
// @Failure 400 {object} map[string]string "缺少 refresh token"
// @Failure 401 {object} map[string]string "refresh token 无效或过期"
// @Router /refresh-token [post]
func (h *UserHomeHandler) RefreshToken(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil {
		// Clear stale cookies to prevent client-side redirect loops
		setAuthCookie(c, "access_token", "", -1)
		setAuthCookie(c, "refresh_token", "", -1)
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing refresh token"})
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	uid, err := strconv.ParseInt(req.UserID, 10, 64)
	if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != req.UserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid identity"})
		return
	}
	status, err := h.CurrentAccountStatus(c.Request.Context(), req.UserID)
	if err != nil || c.Request.Context().Err() != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "AUTH_STATE_UNAVAILABLE", "message": "AUTH_STATE_UNAVAILABLE", "retryable": true, "request_id": c.GetString("request_id")})
		return
	}
	if status == 0 {
		setAuthCookie(c, "access_token", "", -1)
		setAuthCookie(c, "refresh_token", "", -1)
		setAuthCookie(c, middlewares.CSRFCookieName, "", -1)
		c.JSON(http.StatusForbidden, gin.H{"error": "account disabled"})
		return
	}
	newPair, err := h.userService.RefreshAccessToken(req.UserID, refreshToken)
	if err != nil {
		if !errors.Is(err, repo.ErrTokenReused) && !errors.Is(err, repo.ErrInvalidRefreshToken) && !errors.Is(err, redis.Nil) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service unavailable, please retry"})
			return
		}
		// Refresh failed (expired/invalid token) — clear cookies so the
		// frontend interceptor won't keep trying and trigger redirect loops
		setAuthCookie(c, "access_token", "", -1)
		setAuthCookie(c, "refresh_token", "", -1)
		if errors.Is(err, repo.ErrTokenReused) {
			log.Printf("[audit] refresh token reuse detected user=%s", req.UserID)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token invalid or expired"})
		return
	}
	setAuthCookie(c, "access_token", newPair.AccessToken, int(newPair.ExpiresIn))
	setAuthCookie(c, "refresh_token", newPair.RefreshToken, 7*24*60*60)
	middlewares.SetCSRFCookie(c, middlewares.GenerateCSRFToken())
	c.JSON(http.StatusOK, gin.H{"message": "Token refreshed successfully"})
}

// AuthMe 返回当前认证用户信息（供前端 auth/me 判断 role，#1 方案 A）
// AuthMe godoc
// @Summary 获取当前认证用户信息
// @Description 返回当前登录用户的 uid 与角色（user/admin）
// @Tags 用户
// @Produce json
// @Success 200 {object} map[string]interface{} "当前用户信息"
// @Failure 401 {object} map[string]string "未认证"
// @Router /api/v1/auth/me [get]
func (h *UserHomeHandler) AuthMe(c *gin.Context) {
	identity, exists := c.Get("userID")
	userIDStr, isString := identity.(string)
	uid, err := strconv.ParseInt(userIDStr, 10, 64)
	if !exists || !isString || err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != userIDStr {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	role := "user"
	// admin 判断：从 config 白名单读取
	if adminIDs := config.AppConfig.AdminIDs; adminIDs != nil {
		for _, id := range adminIDs {
			if id == userIDStr {
				role = "admin"
				break
			}
		}
	}
	c.JSON(200, gin.H{"uid": userIDStr, "role": role})
}

// UpdateProfile godoc
// @Summary 更新用户资料
// @Tags 用户
// @Accept json
// @Produce json
// @Param body body model.UserProfileRequest true "资料信息"
// @Success 200 {object} map[string]string "更新成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /profile [put]
func (h *UserHomeHandler) UpdateProfile(c *gin.Context) {
	var req model.UserProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	userIDStr, _ := c.Get("userID")
	userID, _ := strconv.ParseInt(userIDStr.(string), 10, 64)
	if err := h.userService.UpdateProfile(c.Request.Context(), userID, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Profile updated successfully"})
}

// GetProfile godoc
// @Summary 获取当前用户资料
// @Tags 用户
// @Produce json
// @Success 200 {object} model.UserProfileResponse "用户资料"
// @Failure 401 {object} map[string]string "未认证"
// @Router /profile [get]
func (h *UserHomeHandler) GetProfile(c *gin.Context) {
	userIDStr, _ := c.Get("userID")
	userID, _ := strconv.ParseInt(userIDStr.(string), 10, 64)
	profile, err := h.userService.GetProfile(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, repo.ErrProfileNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"profile": profile})
}

// ---------- 辅助方法 ----------

func (h *UserHomeHandler) getUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get("userID")
	if !ok {
		return 0, false
	}
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil
}

// ---------- 关系操作 ----------

type targetRequest struct {
	TargetID int64 `json:"target_id" binding:"required"`
}

// Follow godoc
// @Summary 关注用户
// @Tags 社交关系
// @Accept json
// @Produce json
// @Param body body object true "target_id: 被关注用户ID"
// @Success 200 {object} map[string]string "关注成功"
// @Failure 400 {object} map[string]string "参数错误或不能关注自己/已拉黑"
// @Failure 401 {object} map[string]string "未认证"
// @Router /follow [post]
func (h *UserHomeHandler) Follow(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req targetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if h.userService != nil {
		exists, eerr := h.userService.UserExists(c.Request.Context(), req.TargetID)
		if eerr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "relation service unavailable"})
			return
		}
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "target user not found"})
			return
		}
	}
	if err := h.userRelation.Follow(c.Request.Context(), userID, req.TargetID); err != nil {
		writeRelationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "followed"})
	repo.LogAsync(h.behaviorRepo, userID, "follow", strconv.FormatInt(req.TargetID, 10), "user")
}

// Unfollow godoc
// @Summary 取消关注
// @Tags 社交关系
// @Accept json
// @Produce json
// @Param body body object true "target_id: 取消关注用户ID"
// @Success 200 {object} map[string]string "取消成功"
// @Failure 400 {object} map[string]string "参数错误"
// @Failure 401 {object} map[string]string "未认证"
// @Router /unfollow [post]
func (h *UserHomeHandler) Unfollow(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req targetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.userRelation.Unfollow(c.Request.Context(), userID, req.TargetID); err != nil {
		writeRelationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "unfollowed"})
}

// Black godoc
// @Summary 拉黑用户
// @Tags 社交关系
// @Accept json
// @Produce json
// @Param body body object true "target_id: 拉黑用户ID"
// @Success 200 {object} map[string]string "拉黑成功"
// @Failure 401 {object} map[string]string "未认证"
// @Router /black [post]
func (h *UserHomeHandler) Black(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req targetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if h.userService != nil {
		exists, eerr := h.userService.UserExists(c.Request.Context(), req.TargetID)
		if eerr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "relation service unavailable"})
			return
		}
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "target user not found"})
			return
		}
	}
	if err := h.userRelation.Black(c.Request.Context(), userID, req.TargetID); err != nil {
		writeRelationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "blocked"})
}

// Unblack godoc
// @Summary 取消拉黑
// @Tags 社交关系
// @Accept json
// @Produce json
// @Param body body object true "target_id: 取消拉黑用户ID"
// @Success 200 {object} map[string]string "取消成功"
// @Failure 401 {object} map[string]string "未认证"
// @Router /unblack [post]
func (h *UserHomeHandler) Unblack(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req targetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.userRelation.Unblack(c.Request.Context(), userID, req.TargetID); err != nil {
		writeRelationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "unblocked"})
}

// Storage/transaction failures use a stable public response; driver details
// remain in the error chain for internal diagnosis.
func writeRelationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrRelationNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "relation user not found"})
	case errors.Is(err, repo.ErrInvalidRelation):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid relation"})
	case errors.Is(err, repo.ErrRelationBlocked):
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot follow a blocked user"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "relation service unavailable"})
	}
}

// ---------- 关系列表 ----------

// GetFollowers godoc
// @Summary 获取粉丝列表
// @Tags 社交关系
// @Produce json
// @Param offset query int false "偏移量" default(0)
// @Param limit query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "粉丝 ID 列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /followers [get]
func (h *UserHomeHandler) GetFollowers(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	offset, limit := parsePagination(c)
	ids, err := h.userRelation.GetFollowers(c.Request.Context(), userID, offset, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"user_ids": ids,
			"has_more": hasMore,
			"offset":   offset,
			"limit":    limit,
		},
	})
}

// GetFollowing godoc
// @Summary 获取关注列表
// @Tags 社交关系
// @Produce json
// @Param offset query int false "偏移量" default(0)
// @Param limit query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "关注 ID 列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /following [get]
func (h *UserHomeHandler) GetFollowing(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	offset, limit := parsePagination(c)
	ids, err := h.userRelation.GetFollowing(c.Request.Context(), userID, offset, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"user_ids": ids,
			"has_more": hasMore,
			"offset":   offset,
			"limit":    limit,
		},
	})
}

// GetFriends godoc
// @Summary 获取好友列表（互相关注）
// @Tags 社交关系
// @Produce json
// @Param offset query int false "偏移量" default(0)
// @Param limit query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "好友 ID 列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /friends [get]
func (h *UserHomeHandler) GetFriends(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	offset, limit := parsePagination(c)
	ids, err := h.userRelation.GetFriends(c.Request.Context(), userID, offset, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"user_ids": ids,
			"has_more": hasMore,
			"offset":   offset,
			"limit":    limit,
		},
	})
}

// GetBlacklist godoc
// @Summary 获取拉黑列表
// @Tags 社交关系
// @Produce json
// @Param offset query int false "偏移量" default(0)
// @Param limit query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "拉黑 ID 列表"
// @Failure 401 {object} map[string]string "未认证"
// @Router /blacklist [get]
func (h *UserHomeHandler) GetBlacklist(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	offset, limit := parsePagination(c)
	ids, err := h.userRelation.GetBlacklist(c.Request.Context(), userID, offset, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"user_ids": ids,
			"has_more": hasMore,
			"offset":   offset,
			"limit":    limit,
		},
	})
}

// ---------- 关系查询 ----------

func (h *UserHomeHandler) CommonFollowing(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	targetID, err := strconv.ParseInt(c.Query("target_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target_id"})
		return
	}
	common, err := h.userRelation.CommonFollowing(c.Request.Context(), userID, targetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"common_following": common})
}

func (h *UserHomeHandler) IsFollowing(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	targetID, err := strconv.ParseInt(c.Query("target_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target_id"})
		return
	}
	is, err := h.userRelation.IsFollowing(c.Request.Context(), userID, targetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"following": is})
}

func parsePagination(c *gin.Context) (int64, int64) {
	offset, _ := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
	limit, _ := strconv.ParseInt(c.DefaultQuery("limit", "20"), 10, 64)
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	return offset, limit
}

// ---------- 搜索 ----------

// SearchUsers godoc
// @Summary 搜索用户
// @Tags 用户
// @Produce json
// @Param query query string true "搜索关键词"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} model.SearchResponse "搜索结果"
// @Failure 400 {object} map[string]string "缺少 query"
// @Failure 401 {object} map[string]string "未认证"
// @Router /search/users [get]
func (h *UserHomeHandler) SearchUsers(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing query"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	resp, err := h.searchService.SearchUsers(c.Request.Context(), query, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ---------- 公开关系列表接口（无认证，通过 URL 参数指定目标用户）----------

// GetUserFollowers godoc
// @Summary 获取指定用户的粉丝列表（公开接口）
// @Tags 社交关系
// @Produce json
// @Param uid path int true "目标用户ID"
// @Param offset query int false "偏移量" default(0)
// @Param limit query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "粉丝 ID 列表"
// @Failure 400 {object} map[string]string "uid 无效"
// @Router /api/v1/users/{uid}/followers [get]
func (h *UserHomeHandler) GetUserFollowers(c *gin.Context) {
	uid, err := parseUIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	offset, limit := parsePagination(c)
	ids, err := h.userRelation.GetFollowers(c.Request.Context(), uid, offset, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	total, err := h.userRelation.CountFollowers(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"user_ids": ids,
			"total":    total,
			"has_more": hasMore,
			"offset":   offset,
			"limit":    limit,
		},
	})
}

// GetUserFollowing godoc
// @Summary 获取指定用户的关注列表（公开接口）
// @Tags 社交关系
// @Produce json
// @Param uid path int true "目标用户ID"
// @Param offset query int false "偏移量" default(0)
// @Param limit query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "关注 ID 列表"
// @Failure 400 {object} map[string]string "uid 无效"
// @Router /api/v1/users/{uid}/following [get]
func (h *UserHomeHandler) GetUserFollowing(c *gin.Context) {
	uid, err := parseUIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	offset, limit := parsePagination(c)
	ids, err := h.userRelation.GetFollowing(c.Request.Context(), uid, offset, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	total, err := h.userRelation.CountFollowing(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"user_ids": ids,
			"total":    total,
			"has_more": hasMore,
			"offset":   offset,
			"limit":    limit,
		},
	})
}

// parseUIDParam 解析并校验 URL 中的 uid 参数
func parseUIDParam(c *gin.Context) (int64, error) {
	uidStr := c.Param("uid")
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		return 0, errors.New("invalid uid")
	}
	return uid, nil
}
