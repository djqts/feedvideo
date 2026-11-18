package service

import (
	"context"
	"database/sql"
	"errors"
	"feedvideo.local/shared-auth/middlewares"
	"feedvideo.local/user/model"
	"feedvideo.local/user/repo"
	"fmt"
	"log"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// 定义哨兵错误，便于上层调用者识别
var (
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrUserIDNotSet      = errors.New("user ID was not set after creation")
)

// dummyPasswordHash 预计算的 bcrypt 哈希：用户不存在时也执行一次比较，
// 抹平"存在/不存在"的响应耗时差异，防用户名时序枚举。
var dummyPasswordHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("feedvideo-dummy-timing-v1"), bcrypt.DefaultCost)
	if err != nil {
		return nil
	}
	return h
}()

type UserService struct {
	auth    *repo.UserAuthRepo
	profile *repo.UserProfile
	cache   *repo.UserCache
	search  *repo.UserSearch
	jwt     *middlewares.JWTManager

	// 并发控制：限制同时进行的 ES 索引操作数量
	esIndexSemaphore chan struct{}

	// 账号状态短缓存：避免每个认证请求都查库；封禁时由网关钩子 Invalidate 即时失效。
	statusMu    sync.Mutex
	statusCache map[int64]statusCacheEntry
}

type statusCacheEntry struct {
	status int
	expire time.Time
}

const (
	statusCacheTTL = 30 * time.Second
	statusCacheCap = 10000
)

// InfraError 基础设施故障（非认证问题），handler 应映射为 5xx
type InfraError struct{ Err error }

func (e *InfraError) Error() string { return e.Err.Error() }
func (e *InfraError) Unwrap() error { return e.Err }

func NewUserService(
	auth *repo.UserAuthRepo,
	profile *repo.UserProfile,
	cache *repo.UserCache,
	search *repo.UserSearch,
	jwt *middlewares.JWTManager,
) *UserService {
	return &UserService{
		auth:             auth,
		profile:          profile,
		cache:            cache,
		search:           search,
		jwt:              jwt,
		esIndexSemaphore: make(chan struct{}, 10),
		statusCache:      make(map[int64]statusCacheEntry),
	}
}

// IsUserActive 判断用户是否处于可用状态（status!=0：1=正常，2=禁言仍可浏览），带 30s 进程内缓存。
// DB 异常时 fail-open（返回 true），避免把数据库故障放大成全体下线。
func (s *UserService) IsUserActive(ctx context.Context, userID int64) (bool, error) {
	s.statusMu.Lock()
	if e, ok := s.statusCache[userID]; ok && time.Now().Before(e.expire) {
		s.statusMu.Unlock()
		return e.status != 0, nil
	}
	s.statusMu.Unlock()

	user, err := s.auth.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return true, err // fail-open：基础设施故障时放行，交由审计/监控兜底
	}
	status := user.Status

	s.statusMu.Lock()
	if len(s.statusCache) >= statusCacheCap {
		s.statusCache = make(map[int64]statusCacheEntry)
	}
	s.statusCache[userID] = statusCacheEntry{status: status, expire: time.Now().Add(statusCacheTTL)}
	s.statusMu.Unlock()
	return status != 0, nil
}

// IsUserMuted 判断用户是否处于禁言状态（status=2），带同一份 30s 进程内缓存。
// DB 异常 fail-open（返回 false，不误伤可浏览性）。
func (s *UserService) IsUserMuted(ctx context.Context, userID int64) (bool, error) {
	s.statusMu.Lock()
	if e, ok := s.statusCache[userID]; ok && time.Now().Before(e.expire) {
		s.statusMu.Unlock()
		return e.status == 2, nil
	}
	s.statusMu.Unlock()

	user, err := s.auth.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	s.statusMu.Lock()
	if len(s.statusCache) >= statusCacheCap {
		s.statusCache = make(map[int64]statusCacheEntry)
	}
	s.statusCache[userID] = statusCacheEntry{status: user.Status, expire: time.Now().Add(statusCacheTTL)}
	s.statusMu.Unlock()
	return user.Status == 2, nil
}

// UserExists 判断用户是否存在（关注/拉黑等关系写操作前校验目标）。
func (s *UserService) UserExists(ctx context.Context, userID int64) (bool, error) {
	_, err := s.auth.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// AdjustCounter 调整用户计数（关注/粉丝等），由关系服务通过网关钩子调用。
func (s *UserService) AdjustCounter(ctx context.Context, userID int64, field string, delta int64) error {
	if delta == 0 {
		return nil
	}
	return s.cache.IncrUserCounter(ctx, userID, field, delta)
}

// GetFollowerCount 读取粉丝数（counter 链），供创作者中心等模块复用。
func (s *UserService) GetFollowerCount(ctx context.Context, userID int64) (int64, error) {
	c, err := s.cache.GetUserCounter(ctx, userID)
	if err != nil {
		return 0, err
	}
	return c.FollowerCount, nil
}

// SyncFollowerCountToES 关注数变更后写穿 ES 用户文档（best-effort，供搜索排序）。
func (s *UserService) SyncFollowerCountToES(ctx context.Context, userID int64) error {
	if s.search == nil {
		return nil
	}
	count, err := s.GetFollowerCount(ctx, userID)
	if err != nil {
		return err
	}
	user, err := s.auth.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	doc := &model.UserDocument{
		UserID:        userID,
		Username:      user.Username,
		Tags:          []string{},
		FollowerCount: count,
	}
	return s.search.IndexUser(ctx, userID, doc)
}

// InvalidateStatusCache 清除指定用户的账号状态缓存（封禁/解封后调用，使状态即时生效）。
func (s *UserService) InvalidateStatusCache(userID int64) {
	s.statusMu.Lock()
	delete(s.statusCache, userID)
	s.statusMu.Unlock()
}

func (s *UserService) Register(ctx context.Context, req *model.UserRegisterRequest) error {
	if err := validatePasswordStrength(req.Password); err != nil {
		return err
	}
	//用户名唯一性
	existingUser, err := s.auth.GetUserName(ctx, req.Username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("check username failed: %w", err)
	}
	if existingUser != nil {
		return ErrUserAlreadyExists
	}

	// 密码hash
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	// 写入Mysql
	user := &model.User{
		Username:     req.Username,
		PasswordHash: string(hashed),
		Status:       1,
	}
	if err := s.auth.CreateUser(ctx, user); err != nil {
		return fmt.Errorf("create user:%w", err)
	}
	// 防御性检查：确保 User.ID 被正确回填
	if user.ID == 0 {
		return ErrUserIDNotSet
	}

	// 4. 初始化 MongoDB 画像文档（同步调用，失败则返回错误以保证一致性）
	profileData := map[string]interface{}{
		"_id":        user.ID,
		"username":   user.Username,
		"created_at": time.Now(),
		"updated_at": time.Now(),
	}
	if err := s.profile.UpdateProfile(ctx, int64(user.ID), profileData); err != nil {
		// 画像初始化失败，尝试补偿：删除刚创建的 MySQL 用户记录
		if delErr := s.auth.DeleteUser(ctx, int64(user.ID)); delErr != nil {
			log.Printf("compensation failed: unable to delete user %d after profile init error: %v", user.ID, delErr)
		}
		return fmt.Errorf("init profile: %w", err)
	}

	// 5. 索引到 ES（降级为异步 + 仅记录日志）
	doc := &model.UserDocument{
		UserID:        int64(user.ID),
		Username:      user.Username,
		Tags:          []string{},
		FollowerCount: 0,
	}
	// 使用 goroutine 异步执行，避免阻塞注册响应（使用可靠消息队列）
	go func() {
		// 获取信号量，防止无限创建 goroutine
		s.esIndexSemaphore <- struct{}{}
		defer func() { <-s.esIndexSemaphore }()
		// 设置独立的超时上下文（30 秒，涵盖所有重试）
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.indexWithRetry(bgCtx, int64(user.ID), doc); err != nil {
			// 最终失败：记录日志，并可推入死信队列／消息中间件等待后续处理
			log.Printf("warn: index user to ES permanently failed (user_id=%d): %v", user.ID, err)
			// 生产环境可将失败事件写入 Kafka/NSQ 等进行异步恢复
		}
	}()
	// 初始化 Redis 计数（降级，仅记录日志）
	counter := &model.UserCounter{
		UserID:         int64(user.ID),
		FollowerCount:  0,
		FollowingCount: 0,
		PostCount:      0,
	}
	// 同步尝试一次，失败不返回错误
	if err := s.cache.SetUserCounter(ctx, counter); err != nil {
		fmt.Printf("warn: init counter cache failed(user_id=%d): %v\n", user.ID, err)
	}
	return nil
}

// validatePasswordStrength 密码强度：长度 >=8 且同时包含字母与数字。
func validatePasswordStrength(p string) error {
	if len(p) > 72 {
		return fmt.Errorf("password must not exceed 72 UTF-8 bytes")
	}
	if utf8.RuneCountInString(p) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hasLetter, hasDigit := false, false
	for _, c := range p {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("password must contain both letters and digits")
	}
	return nil
}

func (s *UserService) Login(ctx context.Context, req *model.UserLoginRequest) (*middlewares.TokenPair, error) {
	// Historical passwords retain their original strength and whitespace.
	if req.Password == "" || len(req.Password) > 72 {
		return nil, errors.New("password must contain 1 to 72 UTF-8 bytes")
	}
	// 查询用户
	user, err := s.auth.GetUserName(ctx, req.Username)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("invalid username or password")
	}
	if user == nil {
		// 时序均衡：对不存在的用户也执行一次 bcrypt，避免通过耗时枚举用户名
		if dummyPasswordHash != nil {
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
		}
		return nil, errors.New("invalid username or password")
	}
	if !loginAllowed(user.Status) {
		// 统一文案：不暴露"该用户名存在且被禁用"；前端通过 /me 403 展示禁用页。
		// 注意：禁言(status=2)允许登录（可浏览，写端点由 RequireNotMuted 单独拦截），仅封禁(0)拒绝。
		return nil, errors.New("invalid username or password")
	}
	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, errors.New("invalid username or password")
	}

	tokenPair, err := s.jwt.GenerateTokenPair(fmt.Sprintf("%d", user.ID))
	if err != nil {
		return nil, &InfraError{Err: fmt.Errorf("generate token pair: %w", err)}
	}
	return tokenPair, nil
}

// loginAllowed 状态机：仅封禁(0)拒绝登录；正常(1)与禁言(2)均可登录。
func loginAllowed(status int) bool {
	return status != 0
}

func (s *UserService) Logout(ctx context.Context, userID string, refreshToken string) error {
	return s.jwt.DeleteRefreshToken(userID, refreshToken)
}

// GetProfile 获取用户资料
func (s *UserService) GetProfile(ctx context.Context, userID int64) (*model.UserProfileResponse, error) {
	// mongo画像
	profile, err := s.profile.GetProfile(ctx, userID)
	if errors.Is(err, repo.ErrProfileNotFound) {
		// 自愈（验收报告门槛）：老用户/资料丢失时补建默认画像并返回 200，不再 404
		if profile, err = s.ensureProfile(ctx, userID); err != nil {
			return nil, fmt.Errorf("ensure profile: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	if profile == nil {
		profile = &model.MongoUser{}
	}

	var (
		counter    *model.UserCounter
		state      *model.UserOnlineStatus
		isDegraded bool
	)

	// redis计数
	counter, err = s.cache.GetUserCounter(ctx, userID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// 降级处理：使用默认值
			counter = &model.UserCounter{}
			isDegraded = true
		} else {
			return nil, errors.New("user couter not found")
		}
	}
	if counter == nil {
		counter = &model.UserCounter{}
	}
	state, err = s.cache.GetUserOnlineStatus(ctx, userID)
	if err != nil {
		//log 库
		log.Printf("get user:%d online status failed: %v", userID, err)
		isDegraded = true
		state = &model.UserOnlineStatus{}
	} else if state == nil {
		// 仓库在键缺失时返回 (nil, nil)，防御性兜底避免空指针
		state = &model.UserOnlineStatus{}
		isDegraded = true
	}

	// 组装
	resp := &model.UserProfileResponse{
		UserID:         userID,
		Username:       profile.UserName,
		Avatar:         profile.Avatar,
		Bio:            profile.Bio,
		Gender:         profile.Gender,
		City:           profile.City,
		FollowerCount:  counter.FollowerCount,
		FollowingCount: counter.FollowingCount,
		PostCount:      counter.PostCount,
		IsOnline:       state.IsOnline,
		LastSeen:       state.LastSeen.Unix(),
		IsDegraded:     isDegraded,
	}
	// 提取Top标签
	if len(profile.Interests) > 0 {
		tags := make([]string, 0, len(profile.Interests))
		for _, tag := range profile.Interests {
			tags = append(tags, tag.Tag)
		}
		resp.TopTags = tags
	}
	return resp, nil
}

// ensureProfile 为缺失 Mongo 画像的已认证用户 upsert 默认画像。
// username 优先取自 MySQL 认证表；MySQL 不可用时回退 user<id> 仍返回 200。
func (s *UserService) ensureProfile(ctx context.Context, userID int64) (*model.MongoUser, error) {
	username := fmt.Sprintf("user%d", userID)
	if u, err := s.auth.GetUserByID(ctx, userID); err == nil && u.Username != "" {
		username = u.Username
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("warn: get user %d username for profile self-heal failed: %v", userID, err)
	}
	now := time.Now()
	doc := &model.MongoUser{
		UserID:    uint(userID),
		UserName:  username,
		CreatedAt: now,
		UpdatedAt: now,
	}
	fields := map[string]interface{}{
		"_id":        userID,
		"username":   username,
		"created_at": now,
		"updated_at": now,
	}
	if err := s.profile.UpdateProfile(ctx, userID, fields); err != nil {
		return nil, err
	}
	log.Printf("profile self-heal: created default profile for user %d", userID)
	return doc, nil
}

func (s *UserService) UpdateProfile(ctx context.Context, userID int64, req *model.UserProfileRequest) error {

	updateData := map[string]interface{}{}
	// 可用reflect简化代码
	if req.Username != "" {
		// Profile username is the display name; login credentials remain in MySQL.
		updateData["username"] = req.Username
	}
	if req.Avatar != nil {
		updateData["avatar"] = *req.Avatar
	}
	if req.Bio != nil {
		updateData["bio"] = *req.Bio
	}
	if req.Gender != "" {
		updateData["gender"] = req.Gender
	}
	if req.City != "" {
		updateData["city"] = req.City
	}

	if len(updateData) == 0 {
		return nil
	}
	// 加上更新时间
	updateData["updated_at"] = time.Now()

	if err := s.profile.UpdateProfile(ctx, userID, updateData); err != nil {
		return fmt.Errorf("update profile: %w", err)
	}

	// 日志todo: 记录用户更新日志

	return nil
}

func (s *UserService) RefreshAccessToken(userID string, refreshToken string) (*middlewares.TokenPair, error) {
	return s.jwt.RefreshAccessToken(userID, refreshToken)
}

// GetInterests 读取用户兴趣标签（profile.interests 的 tag 列表），供 feed 召回使用。
func (s *UserService) GetInterests(ctx context.Context, userID int64) ([]string, error) {
	profile, err := s.profile.GetProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get interests: %w", err)
	}
	tags := make([]string, 0, len(profile.Interests))
	for _, t := range profile.Interests {
		tags = append(tags, t.Tag)
	}
	return tags, nil
}
