package server

import (
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	creatorHandler "feedvideo.local/creator-center/handler"
	feedHandler "feedvideo.local/feed/handler"
	_ "feedvideo.local/gateway/docs"
	uploadHandler "feedvideo.local/gateway/internal/handler"
	moderationHandler "feedvideo.local/moderation/handler"
	notifHandler "feedvideo.local/notification/handler"
	recommendHandler "feedvideo.local/recommend/handler"
	searchHandler "feedvideo.local/search/handler"
	"feedvideo.local/shared-auth/middlewares"
	config "feedvideo.local/shared-config"
	tagtopicHandler "feedvideo.local/tag-topic/handler"
	userHandler "feedvideo.local/user/handler"
	videoHandler "feedvideo.local/video/handler"
)

func SetUpRouter(
	userH *userHandler.UserHomeHandler,
	videoH *videoHandler.VideoHandler,
	commentH *videoHandler.CommentHandler,
	likeH *videoHandler.LikeHandler,
	feedH *feedHandler.FeedHandler,
	notifH *notifHandler.Handler,
	searchH *searchHandler.Handler,
	tagtopicH *tagtopicHandler.Handler,
	creatorH *creatorHandler.Handler,
	moderationH *moderationHandler.Handler,
	uploadH *uploadHandler.UploadHandler,
	collectH *videoHandler.CollectHandler,
	shareH *videoHandler.ShareHandler,
	adminStatsH *uploadHandler.AdminStatsHandler,
	statsH *uploadHandler.BehaviorStatsHandler,
	recommendH *recommendHandler.RecommendHandler,
	dlqH *uploadHandler.DLQHandler,
	outboxReplayH *uploadHandler.OutboxReplayHandler,
	mediaDLQH *uploadHandler.MediaDLQHandler,
	jwtManager *middlewares.JWTManager,
	healthChecker *middlewares.HealthChecker,
	metrics *middlewares.Metrics,
) *gin.Engine {
	router := gin.New()
	// 信任回环与 Docker 默认网段（compose 中 nginx 所在网络）；
	// nginx 已覆盖 X-Forwarded-For（$remote_addr），客户端无法注入伪造 IP。
	trustedProxies := []string{"127.0.0.1", "::1", "172.16.0.0/12"}
	_ = router.SetTrustedProxies(trustedProxies)

	// Request ID 关联（访问日志/链路追踪）
	router.Use(middlewares.RequestIDMiddleware())

	// CORS middleware — allow cross-origin requests from frontend
	router.Use(middlewares.CORS(middlewares.DefaultCORSConfig()))

	// Access logging middleware — structured request logging
	router.Use(middlewares.AccessLogMiddleware())

	// Metrics middleware — track request counts and latency
	router.Use(metrics.Middleware())
	// Recover inside the access/metrics chain so panic responses are still recorded.
	router.Use(middlewares.RecoveryMiddleware())

	// Swagger 接口文档：生产环境关闭，避免泄露接口细节（完备性说明 §9）
	if !strings.EqualFold(os.Getenv("APP_ENV"), "production") {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// ——— 公开路由（无需认证）———

	// Rate limiting on auth endpoints: 5 requests per minute（Redis 分布式，P2.3）
	router.POST("/register",
		middlewares.RequireSameOrigin(),
		middlewares.RedisRateLimitMiddleware("register", 20, time.Minute), userH.Register)
	router.POST("/login",
		middlewares.RequireSameOrigin(),
		middlewares.RedisRateLimitMiddleware("login", 30, time.Minute),
		middlewares.LoginBackoffLimiter(5, 15*time.Minute),
		userH.Login)

	// CSRF token 下发（P2.1）：API 客户端登录后调用此端点获取最新 token
	router.GET("/csrf-token", middlewares.CSRFTokenHandler())

	// 健康检查分层（P1.1）
	router.GET("/livez", healthChecker.LivezHandler())
	router.GET("/readyz", healthChecker.ReadyzHandler())
	router.GET("/health", healthChecker.HealthHandler())
	router.GET("/metrics", metrics.Handler())

	// refresh 纳入 CSRF 策略（P2.1）：登录后携带 csrf cookie 的客户端必须回传 token
	router.POST("/refresh-token",
		middlewares.RequireSameOrigin(),
		middlewares.RedisRateLimitMiddleware("refresh", 20, time.Minute),
		middlewares.CSRFValidate(), userH.RefreshToken)

	// 视频公开读端点
	router.GET("/api/v1/videos/hot", videoH.GetHotVideos)
	router.GET("/api/v1/videos/search", videoH.SearchVideos)
	router.GET("/api/v1/videos", videoH.ListVideos)

	// 点赞计数（公开）
	router.GET("/api/v1/likes/count", likeH.GetLikeCount)

	// 搜索（公开）
	search := router.Group("/api/v1/search")
	search.Use(searchHandler.SamplingHTTPSMiddleware(trustedProxies))
	if jwtManager != nil {
		search.Use(middlewares.OptionalAuthMiddleware(jwtManager))
	}
	search.GET("", middlewares.RedisRateLimitMiddleware("search", 60, time.Minute), searchH.Search)
	search.GET("/hot", searchH.HotWords)
	search.GET("/suggest", searchH.Suggest)

	// 话题（公开读）
	router.GET("/api/v1/topics/id/:topicId", tagtopicH.GetTopicByID)
	router.GET("/api/v1/topics/:name", tagtopicH.GetTopic)
	router.GET("/api/v1/topics/hot", tagtopicH.HotTopics)

	// 用户关系公开读端点（无需认证，通过 URL 参数指定目标用户）
	router.GET("/api/v1/users/:uid/followers", userH.GetUserFollowers)
	router.GET("/api/v1/users/:uid/following", userH.GetUserFollowing)

	// ——— 可选认证路由 ———
	optional := router.Group("")
	optional.Use(middlewares.OptionalAuthMiddleware(jwtManager))
	{
		optional.GET("/api/v1/videos/:id", videoH.GetVideo)
		optional.GET("/api/v1/videos/:id/comments", commentH.GetComments)
		optional.GET("/api/v1/feed", middlewares.RedisRateLimitMiddleware("feed", 120, time.Minute), feedH.GetFeed)
		optional.GET("/api/v1/recommend/similar/:video_id", recommendH.Similar)
	}

	// ——— 认证路由（JWT 必须）———
	auth := router.Group("")
	auth.Use(middlewares.AuthMiddleware(jwtManager))
	auth.Use(middlewares.CSRFValidate())
	auth.Use(middlewares.RequireCurrentAccount(userH.CurrentAccountStatus))
	{
		// 用户模块（已有）
		auth.GET("/profile", userH.GetProfile)
		auth.PUT("/profile", userH.UpdateProfile)
		auth.POST("/follow", userH.Follow)
		auth.POST("/unfollow", userH.Unfollow)
		auth.POST("/black", userH.Black)
		auth.POST("/unblack", userH.Unblack)
		auth.GET("/followers", userH.GetFollowers)
		auth.GET("/following", userH.GetFollowing)
		auth.GET("/friends", userH.GetFriends)
		auth.GET("/blacklist", userH.GetBlacklist)
		auth.GET("/common-following", userH.CommonFollowing)
		auth.GET("/is-following", userH.IsFollowing)
		auth.GET("/search/users", userH.SearchUsers)
		auth.GET("/api/v1/auth/me", userH.AuthMe)
		registerWatchRoutes(auth, userH.IngestWatch)
		auth.GET("/api/v1/behavior/events", userH.WatchHistory)
		auth.GET("/api/v1/behavior/watch-sessions/:session_id", userH.WatchSession)

		// 视频写端点
		auth.POST("/api/v1/videos", middlewares.RequireNotMuted(userH.IsNotMuted), middlewares.UserRateLimitMiddleware("publish", 10, time.Minute), videoH.Publish)
		auth.PUT("/api/v1/videos/:id", videoH.UpdateVideo)
		auth.DELETE("/api/v1/videos/:id", videoH.DeleteVideo)
		auth.POST("/api/v1/videos/:id/media-retry", middlewares.RequireNotMuted(userH.IsNotMuted), middlewares.UserRateLimitMiddleware("media-retry", 10, time.Minute), videoH.RetryMedia)
		auth.PUT("/api/v1/videos/:id/review", middlewares.RequireAdmin(config.AppConfig.AdminIDs), videoH.ReviewVideo)
		auth.POST("/api/v1/videos/sync-stats", middlewares.RequireAdmin(config.AppConfig.AdminIDs), videoH.BatchSyncStats)

		// 评论端点
		auth.POST("/api/v1/videos/:id/comments", middlewares.RequireNotMuted(userH.IsNotMuted), middlewares.UserRateLimitMiddleware("comment", 60, time.Minute), middlewares.RedisRateLimitMiddleware("comment", 30, time.Minute), commentH.PostComment)
		auth.POST("/api/v1/comments/:id/like", commentH.LikeComment)
		auth.DELETE("/api/v1/comments/:id/like", commentH.UnlikeComment)
		auth.POST("/api/v1/comments/:id/report", middlewares.UserRateLimitMiddleware("report", 20, time.Minute), middlewares.RedisRateLimitMiddleware("report", 10, time.Minute), commentH.ReportComment)
		auth.DELETE("/api/v1/comments/:id", commentH.DeleteComment)

		// 点赞端点
		registerLikeRoutes(auth, likeH, userH.IsNotMuted)

		// Collect & Share
		registerCollectRoutes(auth, collectH, userH.IsNotMuted)
		auth.POST("/api/v1/share/:video_id", middlewares.RequireNotMuted(userH.IsNotMuted), middlewares.UserRateLimitMiddleware("share", 30, time.Minute), middlewares.RedisRateLimitMiddleware("share", 20, time.Minute), shareH.Share)

		// 话题（认证写）
		auth.POST("/api/v1/topics", tagtopicH.CreateTopic)

		// 创作者端点
		auth.GET("/api/v1/creator/summary", creatorH.GetSummary)
		auth.GET("/api/v1/creator/stats", creatorH.GetStats)
		auth.GET("/api/v1/creator/revenue", creatorH.GetRevenue)
		auth.PUT("/api/v1/creator/verify/:uid", middlewares.RequireAdmin(config.AppConfig.AdminIDs), creatorH.VerifyUser)

		// 管理端点（admin）
		auth.GET("/api/v1/admin/videos", middlewares.RequireAdmin(config.AppConfig.AdminIDs), videoH.AdminListVideos)
		auth.PUT("/api/v1/admin/comments/:id/restore", middlewares.RequireAdmin(config.AppConfig.AdminIDs), commentH.AdminRestoreComment)
		auth.GET("/api/v1/admin/reports", middlewares.RequireAdmin(config.AppConfig.AdminIDs), moderationH.GetReports)
		auth.PUT("/api/v1/admin/users/:uid/ban", middlewares.RequireAdmin(config.AppConfig.AdminIDs), moderationH.BanUser)
		auth.PUT("/api/v1/admin/users/:uid/mute", middlewares.RequireAdmin(config.AppConfig.AdminIDs), moderationH.MuteUser)
		auth.POST("/api/v1/admin/notifications/dlq/replay", middlewares.RequireAdmin(config.AppConfig.AdminIDs), dlqH.Replay)
		auth.POST("/api/v1/admin/outbox/replay", middlewares.RequireAdmin(config.AppConfig.AdminIDs), outboxReplayH.Replay)
		auth.POST("/api/v1/admin/media/dlq/replay", middlewares.RequireAdmin(config.AppConfig.AdminIDs), mediaDLQH.Replay)
		auth.GET("/api/v1/admin/stats", middlewares.RequireAdmin(config.AppConfig.AdminIDs), adminStatsH.GetPlatformStats)
		auth.GET("/api/v1/behavior/stats", middlewares.RequireAdmin(config.AppConfig.AdminIDs), statsH.GetStats)
		auth.GET("/api/v1/recommend/for-you", recommendH.ForYou)

		// 通知端点
		auth.GET("/api/v1/notifications", notifH.GetNotifications)
		auth.GET("/api/v1/notifications/unread-count", notifH.GetUnreadCount)
		auth.POST("/api/v1/notifications/read", notifH.MarkRead)
		auth.POST("/api/v1/notifications/system", middlewares.RequireAdmin(config.AppConfig.AdminIDs), notifH.PostSystem)
		auth.DELETE("/api/v1/admin/notifications", middlewares.RequireAdmin(config.AppConfig.AdminIDs), notifH.DeleteNotifications)
		// 视频上传（multipart）
		registerUploadRoutes(auth, uploadH, userH.IsNotMuted)
	}

	// logout 允许被封禁用户执行（清理服务端 refresh token），因此不套 RequireActiveAccount
	logoutGroup := router.Group("")
	logoutGroup.Use(middlewares.OptionalAuthMiddleware(jwtManager))
	logoutGroup.Use(middlewares.RequireSameOrigin())
	logoutGroup.Use(middlewares.CSRFValidate())
	logoutGroup.POST("/logout", userH.Logout)

	return router
}

func registerLikeRoutes(auth *gin.RouterGroup, h *videoHandler.LikeHandler, notMuted middlewares.ActiveChecker) {
	guard := middlewares.RequireNotMuted(notMuted)
	limit := middlewares.UserRateLimitMiddleware("like", 60, time.Minute)
	auth.POST("/api/v1/likes", guard, limit, h.Like)
	auth.PUT("/api/v1/likes", guard, limit, h.SetLikeState)
	auth.DELETE("/api/v1/likes", h.Unlike)
	auth.GET("/api/v1/likes/check", h.IsLiked)
}

func registerCollectRoutes(auth *gin.RouterGroup, h *videoHandler.CollectHandler, notMuted middlewares.ActiveChecker) {
	guard := middlewares.RequireNotMuted(notMuted)
	limit := middlewares.UserRateLimitMiddleware("collect", 60, time.Minute)
	auth.POST("/api/v1/collect", guard, limit, h.Collect)
	auth.PUT("/api/v1/collect", guard, limit, h.SetCollectState)
	auth.GET("/api/v1/collect/check", h.Check)
	auth.GET("/api/v1/collect/list", h.List)
}

func registerUploadRoutes(auth *gin.RouterGroup, h *uploadHandler.UploadHandler, notMuted middlewares.ActiveChecker) {
	guard := middlewares.RequireNotMuted(notMuted)
	auth.POST("/api/v1/upload", guard, middlewares.UserRateLimitMiddleware("upload", 10, time.Minute), middlewares.RedisRateLimitMiddleware("upload", 20, time.Minute), h.Upload)
	auth.GET("/api/v1/uploads/:id", h.GetUpload)
	auth.POST("/api/v1/uploads/:id/renew", guard, middlewares.UserRateLimitMiddleware("upload-renew", 10, time.Minute), h.RenewUpload)
}

func registerWatchRoutes(auth *gin.RouterGroup, ingest gin.HandlerFunc) {
	auth.POST("/api/v1/behavior/events", middlewares.CSRFValidate(), middlewares.UserRateLimitMiddleware("behavior-events", 60, time.Minute), ingest)
}
