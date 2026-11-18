package main

// @title FeedVideo API
// @version 1.0.0
// @description FeedVideo 短视频平台后端接口文档。登录后通过 httpOnly Cookie 携带 JWT；可选认证接口在未登录时降级为公开访问。
// @contact.name FeedVideo Team
// @host localhost:8080
// @BasePath /
// @schemes http https
// @securityDefinitions.apikey CookieAuth
// @in cookie
// @name access_token

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // 注册 pprof handler 到 http.DefaultServeMux，供 startPprof 使用
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	creatorHandler "feedvideo.local/creator-center/handler"
	creatorRepoPkg "feedvideo.local/creator-center/repo"
	creatorService "feedvideo.local/creator-center/service"
	feedHandler "feedvideo.local/feed/handler"
	feedRepoPkg "feedvideo.local/feed/repo"
	feedService "feedvideo.local/feed/service"
	uploadHandler "feedvideo.local/gateway/internal/handler"
	"feedvideo.local/gateway/internal/scheduler"
	"feedvideo.local/gateway/internal/server"
	moderationHandler "feedvideo.local/moderation/handler"
	moderationRepoPkg "feedvideo.local/moderation/repo"
	moderationService "feedvideo.local/moderation/service"
	notifHandler "feedvideo.local/notification/handler"
	notifpub "feedvideo.local/notification/publisher"
	notifRepoPkg "feedvideo.local/notification/repo"
	notifService "feedvideo.local/notification/service"
	recommendHandler "feedvideo.local/recommend/handler"
	recommendService "feedvideo.local/recommend/service"
	searchHandler "feedvideo.local/search/handler"
	searchRepoPkg "feedvideo.local/search/repo"
	searchService "feedvideo.local/search/service"
	"feedvideo.local/shared-auth/middlewares"
	"feedvideo.local/shared-config"
	"feedvideo.local/shared-db/cache"
	"feedvideo.local/shared-db/elastic"
	"feedvideo.local/shared-db/mongo"
	"feedvideo.local/shared-db/outbox"
	db "feedvideo.local/shared-db/sql"
	rabbitmq "feedvideo.local/shared-mq/RabbitMQ"
	storageMod "feedvideo.local/storage"
	tagtopicHandler "feedvideo.local/tag-topic/handler"
	tagtopicRepoPkg "feedvideo.local/tag-topic/repo"
	tagtopicService "feedvideo.local/tag-topic/service"
	userHandler "feedvideo.local/user/handler"
	usermodel "feedvideo.local/user/model"
	userRepo "feedvideo.local/user/repo"
	userService "feedvideo.local/user/service"
	videoHandler "feedvideo.local/video/handler"
	"feedvideo.local/video/orphan"
	videoRepo "feedvideo.local/video/repo"
	videoService "feedvideo.local/video/service"

	"github.com/elastic/go-elasticsearch/v8"
	"go.mongodb.org/mongo-driver/bson"
)

func main() {
	searchProjectionMode, searchProjectionModeErr := config.ParseSearchProjectionMode(os.Getenv("SEARCH_PROJECTION_MODE"))
	if searchProjectionModeErr != nil {
		log.Fatalf("search projection startup failed: %v", searchProjectionModeErr)
	}
	searchProjectionIndex, searchProjectionIndexErr := config.ParseSearchProjectionIndex(os.Getenv("SEARCH_PROJECTION_INDEX"))
	if searchProjectionIndexErr != nil {
		log.Fatalf("search projection startup failed: %v", searchProjectionIndexErr)
	}
	likeMode, likeModeErr := config.ParseLikeStateMode(os.Getenv("LIKE_STATE_MODE"))
	if likeModeErr != nil {
		log.Fatalf("like startup failed: %v", likeModeErr)
	}
	mediaMode, mediaModeErr := config.ParseMediaTaskMode(os.Getenv("MEDIA_TASK_MODE"))
	if mediaModeErr != nil {
		log.Fatalf("media startup failed: %v", mediaModeErr)
	}
	// 1. 加载配置
	cfg, configErr := config.Load("", nil)
	if configErr != nil {
		log.Fatalf("load configuration failed: %v", configErr)
	}
	config.AppConfig = &cfg
	log.Printf("config loaded: app=%s", config.AppConfig.App.Name)
	// 1.1 生产环境 Secret fail-fast（P0.2）
	validateProductionSecrets()
	// 指标收集器提前创建，供各依赖降级点埋点（P1.2）
	metrics := middlewares.NewMetrics()
	// 后台循环统一收敛：信号触发 bgCancel 后各循环退出，bgWg 等待收尾（P2.3/H5）
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	var bgWg sync.WaitGroup

	// 2. 初始化 MySQL（用户模块）
	gormDB, dbErr := db.InitDBWithError()
	if dbErr != nil {
		log.Fatalf("database startup failed: %v", dbErr)
	}
	log.Println("MySQL connected")
	// Development schema changes require an explicit opt-in.
	_, autoMigrate, policyErr := db.MigrationStartupPolicy(config.AppConfig.App.Env, os.Getenv)
	if policyErr != nil {
		log.Fatalf("database startup failed: %v", policyErr)
	}
	if autoMigrate {
		if err := gormDB.AutoMigrate(&usermodel.User{}, &usermodel.UserFollow{}, &usermodel.UserRelationVersion{}); err != nil {
			log.Fatalf("auto migrate failed: %v", err)
		}
	}
	// P3.2：Outbox 表（事件可靠投递）
	outboxStore := outbox.OpenStore(gormDB)
	var err error
	if autoMigrate {
		outboxStore, err = outbox.NewStore(gormDB)
		if err != nil {
			log.Fatalf("outbox init failed: %v", err)
		}
	}
	log.Println("Outbox table ready")
	if err := db.CheckRequiredTables(gormDB, "t_users", "t_user_follows", "user_relation_versions", "outbox_events"); err != nil {
		log.Fatalf("database schema incomplete: %v", err)
	}

	// 3. 初始化 MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mongoDBName := config.AppConfig.MongoDB.Database
	if mongoDBName == "" {
		mongoDBName = "feedvideo"
	}
	mongoInst, err := mongo.Init(ctx, config.AppConfig.MongoDB.URI, mongoDBName)
	if err != nil {
		log.Fatalf("MongoDB init failed: %v", err)
	}
	log.Println("MongoDB connected")

	// 4. 初始化 Redis
	rdb, err := cache.InitRedis(
		config.AppConfig.Redis.Addr,
		config.AppConfig.Redis.Password,
		config.AppConfig.Redis.DB,
	)
	if err != nil {
		log.Fatalf("Redis init failed: %v", err)
	}
	log.Println("Redis connected")
	// P2.3：注册 Redis 分布式限流后端（缺省内存回退）
	middlewares.SetRateLimitRedis(rdb)

	// 4.5 上传孤儿回收跟踪器（storage 在 12.10 初始化后挂接）
	uploadTracker := orphan.NewTracker(rdb, nil, orphan.DefaultTTL)

	// 5. 初始化 Elasticsearch（可选）
	var esRepo *elastic.ESRepo
	var esRawClient *elasticsearch.Client
	var sensitiveChecker videoService.SensitiveChecker
	esClient, esInitErr := initializeSearchProjectionES(searchProjectionMode, config.AppConfig.ES.Addresses, elastic.Init, elastic.InitProjection)
	if esInitErr != nil {
		if searchProjectionMode == config.SearchProjectionModeMongo {
			log.Fatalf("search projection ES configuration failed: %v", esInitErr)
		}
		log.Printf("warning: ES init failed (non-fatal): %v", esInitErr)
		metrics.IncFallback("es")
	} else if esClient != nil {
		esRepo = elastic.NewESRepo(&elastic.ElasticClient{Client: esClient})
		esRawClient = esClient
		if searchProjectionMode == config.SearchProjectionModeLegacy {
			log.Println("Elasticsearch connected")
			idxCtx, idxCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := esRepo.EnsureDefaultIndexes(idxCtx); err != nil {
				log.Printf("warning: ensure default es indexes: %v", err)
			}
			if err := esRepo.SeedSensitiveWords(idxCtx, elastic.DefaultSensitiveWords); err != nil {
				log.Printf("warning: seed sensitive words: %v", err)
			}
			idxCancel()
		} else {
			log.Println("Elasticsearch projection client configured")
		}
		sensitiveChecker = videoService.NewESSensitiveChecker(esRepo)
	} else {
		log.Println("Elasticsearch not configured, skipping")
	}

	// 6. 创建 Video Repo
	mongoDB := mongoInst.Database()
	// 孤儿回收对账：对象若被视频引用（发布成功但 Claim 失败/迟到），回收器不得删除
	uploadTracker.SetReferencedChecker(func(ctx context.Context, bucket, object string) bool {
		url := "/storage/" + bucket + "/" + object
		filter := bson.M{"$or": []bson.M{
			{"video_url": url},
			{"cover_url": url},
			{"transcoded_url": url},
			{"transcoded_url_720p": url},
		}}
		n, err := mongoDB.Collection("videos").CountDocuments(ctx, filter)
		return err == nil && n > 0
	})
	vRepo := videoRepo.NewVideoRepoMongo(mongoDB, rdb, esRepo)
	searchSource, searchSourceSupported := vRepo.(searchProjectionSource)
	if !searchSourceSupported {
		log.Fatal("search projection source configuration unavailable")
	}
	searchProjection, err := prepareSearchProjection(context.Background(), searchProjectionMode, searchProjectionIndex, searchSource,
		func() videoRepo.SearchProjectionStore { return videoRepo.NewSearchProjectionRepo(mongoDB) },
		func(index string) scheduler.SearchProjectionSink {
			return videoRepo.NewSearchProjectionIndexer(esRepo, index)
		})
	if err != nil {
		log.Fatalf("search projection startup failed: %v", err)
	}
	uploadStore := videoRepo.NewUploadRepo(mongoDB)
	publishStore, publishSupported := vRepo.(videoRepo.PublishStore)
	if !publishSupported {
		log.Fatal("upload publication persistence unavailable")
	}
	if err := prepareUploadIndexes(context.Background(), uploadStore, publishStore); err != nil {
		log.Fatalf("upload/publish indexes incomplete: %v", err)
	}
	mediaJobStore := videoRepo.NewMediaJobRepo(mongoDB)
	if err := prepareMediaJobIndexes(context.Background(), mediaMode, mediaJobStore); err != nil {
		log.Fatalf("media job indexes incomplete: %v", err)
	}

	// 索引归属 video 模块，best-effort 不阻塞启动（feed-flow）
	idxCtx, idxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := vRepo.EnsureIndexes(idxCtx); err != nil {
		log.Printf("warning: ensure video indexes: %v", err)
	}
	idxCancel()

	cRepo := videoRepo.NewCommentRepoMongo(mongoDB, rdb, esRepo)
	if err := cRepo.EnsureIndexes(ctx); err != nil {
		log.Printf("warning: ensure comments indexes: %v", err)
	}
	lRepo := videoRepo.NewLikeRepoMongo(mongoDB, rdb)
	var likeStateStore videoRepo.LikeStateStore
	if likeMode == config.LikeStateModeMongo {
		likeStateStore = videoRepo.NewLikeStateRepo(mongoDB)
		if err := prepareLikeStateIndexes(context.Background(), likeMode, likeStateStore); err != nil {
			log.Fatalf("like state indexes incomplete: %v", err)
		}
		lRepo = videoRepo.NewDurableLikeAdapter(likeStateStore)
		vRepo.(*videoRepo.VideoRepoMongo).SetDurableLikes(true)
	}

	// 6.5 通知模块基础设施（读 API 始终可用，不依赖 Broker）
	notifStore := notifRepoPkg.NewMongoStore(mongoDB)
	notifIdxCtx, notifIdxCancel := context.WithTimeout(context.Background(), 15*time.Second)
	notifIdxErr := notifStore.EnsureIndexes(notifIdxCtx)
	notifIdxCancel()
	if notifIdxErr != nil {
		log.Fatalf("notification indexes incomplete: %v", notifIdxErr)
	}
	notifUnread := notifRepoPkg.NewRedisUnread(rdb)
	notifSvc := notifService.NewService(notifStore, notifUnread)
	notifAdminIDs := append(append([]string{}, config.AppConfig.AdminIDs...), config.AppConfig.Notification.AdminIDList()...)
	notifH := notifHandler.NewHandler(notifSvc, notifAdminIDs)
	// 系统通知广播全部：从 MySQL 用户表解析全量用户 ID（P4 管理端闭环）
	notifH.SetAllUsersProvider(func() ([]string, error) {
		var ids []int64
		if err := gormDB.Model(&usermodel.User{}).Where("status = ?", 1).Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, strconv.FormatInt(id, 10))
		}
		return out, nil
	})

	// 6.6 Broker 可选启动：成功 → RabbitPublisher（通知消费由独立 notification-worker 容器承担）；失败 → Outbox 挂起 + 降级指标
	// Legacy observations use one bounded read batch; unsupported real-time
	// gauges remain unknown rather than treating cached events as current state.
	metrics.SetSearchProjectionReader(searchProjection.metricsSnapshot)
	installLegacyMetrics(metrics, rdb, mediaMode)
	metrics.SetDBPoolReader(func() float64 {
		sqlDB, err := gormDB.DB()
		if err != nil {
			return 0
		}
		st := sqlDB.Stats()
		if st.MaxOpenConnections <= 0 {
			return 0
		}
		return float64(st.InUse) / float64(st.MaxOpenConnections)
	})
	var videoNotifier videoService.Notifier = notifpub.NoopPublisher{}
	var userNotifier userService.Notifier = notifpub.NoopPublisher{}
	// P3.2：业务事件统一先写 Outbox（Broker 故障不丢事件），由 relay 幂等投递
	videoNotifier = notifpub.NewOutboxNotifier(outboxStore)
	userNotifier = notifpub.NewOutboxNotifier(outboxStore)
	var broker *rabbitmq.Broker
	mqConfig := rabbitmq.Config{
		URL:     config.AppConfig.RabbitMQ.Addr,
		VHost:   config.AppConfig.RabbitMQ.VHost,
		Metrics: mqMetricsAdapter{m: metrics},
	}
	connectBroker := func() (*rabbitmq.Broker, error) {
		brokerCtx, brokerCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer brokerCancel()
		b, err := rabbitmq.NewBroker(brokerCtx, mqConfig)
		if err != nil {
			return nil, err
		}
		topoCtx, topoCancel := context.WithTimeout(context.Background(), 10*time.Second)
		topoErr := b.DeclareTopology(topoCtx, "notifications", "notifications_dlq")
		topoCancel()
		if topoErr != nil {
			_ = b.Close(context.Background())
			return nil, topoErr
		}
		return b, nil
	}
	if b, err := connectBroker(); err != nil {
		log.Printf("warning: rabbitmq unavailable, notifications degraded: %v", err)
		metrics.IncFallback("mq")
	} else {
		broker = b
		log.Println("RabbitMQ connected, notification publisher ready")
	}
	// P3.2：Outbox relay —— 把 pending 事件投递到 RabbitMQ。
	// Broker 启动时不可用不代表永久不可用：relay 循环会带退避重连，
	// 连上后自行启动投递，避免“先起 gateway 再起 MQ”时通知永久积压。
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		current := broker
		for {
			if current == nil {
				select {
				case <-bgCtx.Done():
					return
				case <-time.After(15 * time.Second):
				}
				b, err := connectBroker()
				if err != nil {
					metrics.IncFallback("mq")
					log.Printf("[outbox] rabbitmq retry failed: %v", err)
					continue
				}
				current = b
				log.Println("[outbox] RabbitMQ connected after retry, notification relay started")
			}
			scheduler.RunOutboxRelayLoop(bgCtx, outboxStore, current, rdb, 5*time.Second)
			if current != broker {
				_ = current.Close(context.Background())
			}
			return
		}
	}()

	// 7. 创建 Video Service（注入 notifier）
	// 7a. 创建 Behavior Repo
	behaviorRepo := userRepo.NewUserBehaviorRepo(mongoDB.Collection("behavior_logs"))

	// 7b. 创建 Video Service（注入 notifier）
	videoSvc := videoService.NewVideoService(vRepo, lRepo)
	videoSvc.SetSensitiveChecker(sensitiveChecker)
	commentSvc := videoService.NewCommentService(cRepo, vRepo, videoNotifier)
	commentSvc.SetSensitiveChecker(sensitiveChecker)
	likeSvc := videoService.NewLikeService(lRepo, vRepo, cRepo, videoNotifier)
	if likeStateStore != nil {
		likeSvc.SetStateStore(likeStateStore)
	}
	commentSvc.SetLikeService(likeSvc)

	// 8. 创建 Video Handler
	videoH := videoHandler.NewVideoHandler(videoSvc, rdb, behaviorRepo, uploadTracker)
	commentH := videoHandler.NewCommentHandler(commentSvc)
	likeH := videoHandler.NewLikeHandler(likeSvc, behaviorRepo)

	// 9. 创建 User 模块依赖
	userAuthRepo := userRepo.NewUserAuthRepo(gormDB)
	userProfileRepo := userRepo.NewUserProfile(mongoDB.Collection("users"))
	userCacheRepo := userRepo.NewUserCache(rdb.Client)
	userSearchRepo := userRepo.NewUserSearch(esRawClient, "users")

	// 10. 加载或生成 RSA 密钥对
	// 生产：必须通过 Secret 注入密钥文件路径（Docker Secret / K8s Secret），缺失即 fail-fast。
	// 开发：密钥缺失时自动生成（本地快速体验）。
	privateKeyPath := os.Getenv("PRIVATE_KEY_PATH")
	publicKeyPath := os.Getenv("PUBLIC_KEY_PATH")
	if privateKeyPath == "" {
		privateKeyPath = "private.pem"
	}
	if publicKeyPath == "" {
		publicKeyPath = "public.pem"
	}
	privateKey, err := middlewares.LoadPrivateKey(privateKeyPath)
	if err != nil {
		if config.AppConfig.App.Env == "production" {
			log.Fatalf("production: RSA private key missing at %s — inject via PRIVATE_KEY_PATH (Docker/K8s Secret)", privateKeyPath)
		}
		log.Printf("warning: cannot load %s, generating new RSA keys...", privateKeyPath)
		if err := generateRSAKeyPair(); err != nil {
			log.Fatalf("generate RSA keys failed: %v", err)
		}
		privateKeyPath = "private.pem"
		publicKeyPath = "public.pem"
		privateKey, err = middlewares.LoadPrivateKey(privateKeyPath)
		if err != nil {
			log.Fatalf("load private key failed: %v", err)
		}
	}
	publicKey, err := middlewares.LoadPublicKey(publicKeyPath)
	if err != nil {
		if config.AppConfig.App.Env == "production" {
			log.Fatalf("production: RSA public key missing at %s — inject via PUBLIC_KEY_PATH", publicKeyPath)
		}
		log.Fatalf("load public key failed: %v", err)
	}

	// 11. 创建 JWT Manager
	refreshStore := userRepo.NewRefreshStore(rdb.Client)
	jwtManager, err := middlewares.NewJWTManager(
		privateKey,
		publicKey,
		15*time.Minute,
		7*24*time.Hour,
		refreshStore,
	)
	if err != nil {
		log.Fatalf("create JWT manager failed: %v", err)
	}

	// 12. 创建 User Service + Handler
	relationMysqlRepo := userRepo.NewRelation(gormDB)
	relationCacheRepo := userRepo.NewRelationCacheRepo(rdb.Client)
	relationSvc := userService.NewRelationService(relationMysqlRepo, relationCacheRepo, userNotifier)
	searchSvc := userService.NewSearchService(userSearchRepo)

	userSvc := userService.NewUserService(
		userAuthRepo, userProfileRepo, userCacheRepo, userSearchRepo, jwtManager,
	)
	// 关注/粉丝计数：关系写路径 → Redis 原子计数（best-effort，失败仅日志）
	relationSvc.SetCounterUpdater(func(ctx context.Context, uid int64, field string, delta int64) {
		if err := userSvc.AdjustCounter(ctx, uid, field, delta); err != nil {
			log.Printf("[counter] adjust failed uid=%d field=%s delta=%d err=%v", uid, field, delta, err)
		}
		// 关注数变更写穿 ES 用户文档（best-effort）
		if field == "follower_count" {
			if err := userSvc.SyncFollowerCountToES(ctx, uid); err != nil {
				log.Printf("[counter] sync follower_count to ES failed uid=%d err=%v", uid, err)
			}
		}
	})
	userH := userHandler.NewUserHomeHandler(userSvc, relationSvc, searchSvc, behaviorRepo)
	watchStore := userRepo.NewWatchRepo(mongoDB)
	if err := prepareWatchIndexes(context.Background(), watchStore); err != nil {
		log.Fatalf("watch indexes incomplete: %v", err)
	}
	watchReader, watchSupported := vRepo.(watchCurrentReader)
	if !watchSupported {
		log.Fatal("watch media source unavailable")
	}
	watchSvc := userService.NewWatchService(watchStore, watchMediaSource(watchReader))
	userH.WithWatchHandler(userHandler.NewWatchHandler(watchSvc, userSvc.WatchAccountActive).WithQueries(userService.NewWatchQueryService(watchStore)))

	// 12.5 创建 Feed 模块（userSvc=InterestProvider，relationSvc=可回源的 FollowProvider）
	feedRepo := feedRepoPkg.NewFeedRepo(rdb, config.AppConfig.Feed)
	feedSvc := feedService.NewFeedService(vRepo, userSvc, relationSvc, feedRepo, config.AppConfig.Feed)
	if err := startFeedBackground(bgCtx, &bgWg, feedSvc); err != nil {
		log.Fatalf("feed background startup failed: %v", err)
	}
	feedH := feedHandler.NewFeedHandler(feedSvc)
	feedH.SetVideoService(videoSvc)

	// 12.6 创建 Search 模块（注入 vRepo, userSvc, searchRepo；tag 搜索用 SearchByTags）
	searchRepo := searchRepoPkg.NewSearchRepo(rdb, config.AppConfig.Search)
	searchSvc2 := searchService.NewSearchService(vRepo, searchSvc, searchRepo, config.AppConfig.Search)
	searchProjection.configureCache(searchSvc2)
	if err := startSearchBackground(bgCtx, &bgWg, searchSvc2, metrics); err != nil {
		log.Fatal("search background startup failed")
	}
	searchH := searchHandler.NewHandler(searchSvc2)

	// 12.7 创建 Tag-Topic 模块
	tagtopicRepo := tagtopicRepoPkg.NewTopicRepo(mongoDB, rdb)
	tagtopicIdxCtx, tagtopicIdxCancel := context.WithTimeout(context.Background(), 15*time.Second)
	_ = tagtopicRepo.EnsureIndexes(tagtopicIdxCtx)
	tagtopicIdxCancel()
	tagtopicSvc := tagtopicService.NewTagTopicService(tagtopicRepo, vRepo)
	tagtopicH := tagtopicHandler.NewHandler(tagtopicSvc)
	videoSvc.SetTopicNotifier(tagtopicSvc)

	// 12.8 创建 Creator-Center 模块
	creatorRepo := creatorRepoPkg.NewCreatorRepo(rdb, vRepo)
	creatorSvc := creatorService.NewCreatorService(creatorRepo, userProfileRepo, config.AppConfig.Creator, config.AppConfig.AdminIDs)
	configureCreatorVerificationTarget(creatorSvc, userAuthRepo)
	// Creator statistics use persisted relations, independent of counter cache loss.
	creatorSvc.SetFollowerCountProvider(func(ctx context.Context, uid string) (int64, error) {
		uidInt, err := strconv.ParseInt(uid, 10, 64)
		if err != nil {
			return 0, err
		}
		return relationSvc.CountFollowers(ctx, uidInt)
	})
	creatorH := creatorHandler.NewHandler(creatorSvc)

	// 12.9 创建 Moderation 模块
	moderationRepo := moderationRepoPkg.NewModerationRepo(esRepo, userAuthRepo, vRepo, cRepo)
	moderationSvc := moderationService.NewModerationService(moderationRepo)
	// 状态机：封禁(0)吊销 refresh 族；解封/解禁(1)清除吊销标记恢复刷新；任何变更清状态缓存
	moderationSvc.SetUserStatusHook(func(uid int64, status int) {
		uidStr := strconv.FormatInt(uid, 10)
		hctx, hcancel := context.WithTimeout(context.Background(), time.Second)
		defer hcancel()
		switch status {
		case 0:
			_ = rdb.Set(hctx, userRepo.RefreshRevokedPrefix+uidStr, "1", 7*24*time.Hour)
		case 1:
			_ = rdb.Del(hctx, userRepo.RefreshRevokedPrefix+uidStr)
		}
		userSvc.InvalidateStatusCache(uid)
	})
	moderationH := moderationHandler.NewHandler(moderationSvc, config.AppConfig.AdminIDs)

	// 12.9b 初始化 Collect & Share
	collectRepo := videoRepo.NewCollectRepoMongo(mongoDB, rdb)
	if err := prepareCollectStateIndexes(context.Background(), collectRepo); err != nil {
		log.Fatalf("collect state indexes: %v", err)
	}
	if durableVideoRepo, ok := vRepo.(*videoRepo.VideoRepoMongo); ok {
		durableVideoRepo.SetDurableCollections(true)
	}
	collectSvc := videoService.NewCollectService(collectRepo, vRepo)
	videoSvc.SetCollectRepo(collectRepo)
	collectSvc.SetVideoService(videoSvc)
	collectH := videoHandler.NewCollectHandler(collectSvc, behaviorRepo)
	shareH := videoHandler.NewShareHandler(vRepo, behaviorRepo)

	// 12.9c Admin Stats Handler
	adminStatsH := uploadHandler.NewAdminStatsHandler(mongoDB.Collection("videos"), gormDB, rdb)
	statsH := uploadHandler.NewBehaviorStatsHandler(behaviorRepo).WithAdminIDs(config.AppConfig.AdminIDs)

	// 12.9d Recommend Engine
	recommendSvc := recommendService.New(mongoDB, rdb)
	recommendH := recommendHandler.New(recommendSvc)
	recommendH.SetVideoService(videoSvc)

	// 12.10 初始化 MinIO Storage + Upload Handler
	stCfg := config.LoadStorageConfig()
	storageClient, err := storageMod.NewMinioStorage(&stCfg)
	if err != nil {
		log.Fatalf("minio storage init: %v", err)
	}
	if err := storageClient.EnsureBuckets(context.Background(), []string{stCfg.VideoBucket, stCfg.CoverBucket}); err != nil {
		log.Fatalf("ensure buckets: %v", err)
	}
	log.Println("MinIO storage initialized, buckets ensured")
	videoSvc.ConfigureMediaJobs(mediaMode == config.MediaTaskModeMongo, stCfg.VideoBucket, storageClient)
	videoSvc.SetMediaJobStore(mediaJobStore)
	videoSvc.ConfigureUploads(uploadStore, publishStore, stCfg.VideoBucket, stCfg.CoverBucket, storageClient)
	uploadTracker.SetStorage(storageClient)
	// P3.3：删除视频时异步清理 MinIO 对象（原片/转码/封面/缩略图）
	videoSvc.SetAssetCleaner(func(ctx context.Context, urls []string) {
		for _, u := range urls {
			bucket, object, ok := orphan.ParseStorageURL(u)
			if !ok {
				continue
			}
			if err := storageClient.Delete(ctx, bucket, object); err != nil {
				log.Printf("[asset-cleanup] delete %s/%s failed: %v", bucket, object, err)
			} else {
				log.Printf("[asset-cleanup] deleted %s/%s", bucket, object)
			}
		}
	})
	uploadH := uploadHandler.NewUploadHandler(storageClient, &stCfg, uploadTracker).
		WithUploadStore(uploadStore).
		WithMetrics(metrics).
		WithStatusHook(func(status string) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_, _ = rdb.Incr(ctx, "metrics:video:status:"+status)
		})
	dlqH := uploadHandler.NewDLQHandler(broker)
	outboxReplayH := uploadHandler.NewOutboxReplayHandler(outboxStore)
	mediaDLQH := uploadHandler.NewMediaDLQHandler(uploadHandler.NewMediaDLQRedis(rdb))
	mediaDLQH.SetDurableMode(mediaMode == config.MediaTaskModeMongo)

	// 12.10b 后台任务：上传孤儿回收 + 热点统计自动同步（STATS_SYNC_INTERVAL_SECONDS，0=禁用）
	uploadTracker.SetReapHook(func(n int) {
		for i := 0; i < n; i++ {
			metrics.IncUploadOrphan()
		}
	})
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		uploadTracker.ReapLoop(bgCtx, orphan.DefaultInterval)
	}()
	statsInterval := 10 * time.Minute
	if secs, err := strconv.Atoi(os.Getenv("STATS_SYNC_INTERVAL_SECONDS")); err == nil && secs > 0 {
		statsInterval = time.Duration(secs) * time.Second
	}
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		scheduler.RunStatsSyncLoop(bgCtx, rdb, videoSvc, vRepo, statsInterval)
	}()
	if likeMode == config.LikeStateModeMongo {
		projection := videoRepo.NewLikeProjectionRepo(mongoDB, rdb)
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			scheduler.RunLikeStateLoop(bgCtx, likeStateStore, projection, videoNotifier)
		}()
	} else if likeSyncer, ok := lRepo.(scheduler.LikePersister); ok {
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			scheduler.RunLikeSyncLoop(bgCtx, rdb, likeSyncer, lRepo, statsInterval)
		}()
	}
	searchProjection.start(bgCtx, &bgWg)

	// 12.11 创建 HealthChecker + Metrics
	healthChecker := &middlewares.HealthChecker{
		MySQLDB:     gormDB,
		MongoDB:     mongoDB,
		RedisClient: rdb.Client,
		Extra:       map[string]middlewares.Pinger{},
	}
	if esRepo != nil {
		healthChecker.Extra["elasticsearch"] = esPinger{es: esRepo}
	}
	if storageClient != nil {
		healthChecker.Extra["minio"] = storageClient
	}
	if broker != nil {
		healthChecker.Extra["rabbitmq"] = broker
	}
	// 13. 注册路由
	router := server.SetUpRouter(userH, videoH, commentH, likeH, feedH, notifH, searchH, tagtopicH, creatorH, moderationH, uploadH, collectH, shareH, adminStatsH, statsH, recommendH, dlqH, outboxReplayH, mediaDLQH, jwtManager, healthChecker, metrics)

	// 13.5 pprof 可观测性（默认 127.0.0.1:6060，PPROF_DISABLE=1 关闭，PPROF_ADDR 覆盖地址）
	pprofSrv := startPprof()

	// 14. 启动 HTTP Server（支持优雅退出）
	port := config.AppConfig.App.Port
	if port == "" {
		port = ":8080"
	}
	srv := &http.Server{
		Addr:         port,
		Handler:      router,
		ReadTimeout:  300 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		log.Printf("FeedVideo starting on %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	// 1) 先停后台循环（Outbox relay / 统计同步 / 点赞同步 / 孤儿回收 / 搜索投影）
	bgCancel()
	// 2) 停止 HTTP / pprof / Broker
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if pprofSrv != nil {
		_ = pprofSrv.Shutdown(shutdownCtx)
	}
	if broker != nil {
		_ = broker.Close(shutdownCtx)
	}
	_ = srv.Shutdown(shutdownCtx)
	// 3) 等待后台循环收敛（最多 5s；超出仅告警，不阻塞退出）
	loopsDone := make(chan struct{})
	go func() {
		bgWg.Wait()
		close(loopsDone)
	}()
	select {
	case <-loopsDone:
		log.Println("background loops stopped")
	case <-time.After(5 * time.Second):
		log.Println("warning: background loops did not stop within 5s")
	}
	// 4) 行为日志 worker 排空后关闭
	type behaviorCloser interface{ Close() }
	if f, ok := any(behaviorRepo).(behaviorCloser); ok {
		f.Close()
	}
	type behaviorWaiter interface{ Wait(time.Duration) bool }
	if w, ok := any(behaviorRepo).(behaviorWaiter); ok {
		if !w.Wait(3 * time.Second) {
			log.Println("warning: behavior worker drain timeout")
		}
	}
	log.Println("bye")
}

func prepareLikeStateIndexes(ctx context.Context, mode string, store videoRepo.LikeStateStore) error {
	if mode != config.LikeStateModeMongo {
		return nil
	}
	if store == nil {
		return fmt.Errorf("like state store unavailable")
	}
	indexCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return store.EnsureIndexes(indexCtx)
}

func prepareCollectStateIndexes(ctx context.Context, store videoRepo.CollectStateStore) error {
	if store == nil {
		return fmt.Errorf("collect state store unavailable")
	}
	indexCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return store.EnsureIndexes(indexCtx)
}

// startPprof 启动独立的 pprof HTTP 服务（与业务端口隔离），返回 server 供优雅退出。
func startPprof() *http.Server {
	if os.Getenv("PPROF_DISABLE") == "1" {
		return nil
	}
	addr := os.Getenv("PPROF_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6060"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           http.DefaultServeMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("pprof listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("pprof server failed: %v", err)
		}
	}()
	return srv
}

// validateProductionSecrets 在生产模式下对缺失/弱凭据 fail-fast（P0.2）。
// 本地开发（development）不阻塞启动，便于离线快速体验。
func validateProductionSecrets() {
	if config.AppConfig.App.Env != "production" {
		return
	}
	type secretCheck struct {
		env  string
		name string
	}
	required := []secretCheck{
		{"MYSQL_PASSWORD", "MySQL password"},
		{"REDIS_PASSWORD", "Redis password"},
		{"MINIO_ACCESS_KEY", "MinIO access key"},
		{"MINIO_SECRET_KEY", "MinIO secret key"},
		{"RABBITMQ_URL", "RabbitMQ URL"},
		{"JWT_SECRET_KEY", "JWT secret"},
		{"PRIVATE_KEY_PATH", "RSA private key path"},
		{"PUBLIC_KEY_PATH", "RSA public key path"},
	}
	missing := make([]string, 0, len(required))
	for _, r := range required {
		if os.Getenv(r.env) == "" {
			missing = append(missing, r.name+"("+r.env+")")
		}
	}
	if len(missing) > 0 {
		log.Fatalf("production: missing required secrets, refusing to start: %v", missing)
	}

	// 防御性检查：禁止仍在使用文档中的默认/示例凭据
	defaults := []struct {
		env     string
		example string
	}{
		{"MYSQL_PASSWORD", "feedvideo123"},
		{"MINIO_SECRET_KEY", "minioadmin"},
		{"RABBITMQ_URL", "amqp://guest:guest@"},
		{"JWT_SECRET_KEY", "feedvideo-jwt-secret"},
	}
	for _, d := range defaults {
		v := os.Getenv(d.env)
		if v == d.example || (d.env == "RABBITMQ_URL" && strings.Contains(v, d.example)) {
			log.Fatalf("production: secret %s still uses a known default/example value, refusing to start", d.env)
		}
	}
	log.Println("production: required secrets verified")
}

// mqMetricsAdapter forwards RabbitMQ client metrics into the Prometheus
// /metrics endpoint so publish failures are observable instead of silent.
type mqMetricsAdapter struct {
	m *middlewares.Metrics
}

func (a mqMetricsAdapter) IncSendSuccess()        { a.m.IncMQSendSuccess() }
func (a mqMetricsAdapter) IncSendFailure()        { a.m.IncMQSendFailure() }
func (a mqMetricsAdapter) IncReceiveCount()       { a.m.IncMQReceived() }
func (a mqMetricsAdapter) SetConsumerGauge(n int) { a.m.SetMQConsumers(n) }
func (a mqMetricsAdapter) ObserveMQPublishLatency(seconds float64) {
	a.m.ObserveMQPublishLatency(seconds)
}
func (a mqMetricsAdapter) ObserveMQConsumeLatency(seconds float64) {
	a.m.ObserveMQConsumeLatency(seconds)
}

// esPinger 适配 Elasticsearch 到 HealthChecker.Pinger。
type esPinger struct {
	es *elastic.ESRepo
}

func (p esPinger) Ping(ctx context.Context) error {
	return p.es.Client.PingContext(ctx)
}

// generateRSAKeyPair 生成 RSA-2048 密钥对并写入 private.pem / public.pem
func generateRSAKeyPair() error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate RSA key: %w", err)
	}

	privateFile, err := os.Create("private.pem")
	if err != nil {
		return fmt.Errorf("create private.pem: %w", err)
	}
	defer privateFile.Close()

	if err := pem.Encode(privateFile, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}); err != nil {
		return fmt.Errorf("encode private key: %w", err)
	}

	publicFile, err := os.Create("public.pem")
	if err != nil {
		return fmt.Errorf("create public.pem: %w", err)
	}
	defer publicFile.Close()

	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	if err := pem.Encode(publicFile, &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	}); err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}

	log.Println("RSA key pair generated: private.pem, public.pem")
	return nil
}

// prepareUploadIndexes is a required startup gate even in legacy media mode:
// publication idempotency and durable uploads do not depend on worker mode.
func prepareUploadIndexes(ctx context.Context, uploads videoRepo.UploadStore, published videoRepo.PublishStore) error {
	if uploads == nil || published == nil {
		return fmt.Errorf("upload/publish persistence unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := uploads.EnsureIndexes(ctx); err != nil {
		return err
	}
	return published.EnsurePublishIndexes(ctx)
}
