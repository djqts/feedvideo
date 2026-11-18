package service

import (
	"context"
	"log/slog"
	"time"

	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
	"feedvideo.local/user/repo"
)

// Notifier 由 gateway 注入的通知发布器。user 只依赖此最小接口，不 import notification。
type Notifier interface {
	Publish(ctx context.Context, evt events.Event) error
}

// Relation 服务接口
type Relation interface {
	GetFollowers(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetFollowing(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetBlacklist(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	GetFriends(ctx context.Context, userID int64, offset, limit int64) ([]int64, error)
	Follow(ctx context.Context, followerID, followingID int64) error
	Unfollow(ctx context.Context, followerID, followingID int64) error
	Black(ctx context.Context, userID, targetID int64) error
	Unblack(ctx context.Context, userID, targetID int64) error
	CommonFollowing(ctx context.Context, userID, targetID int64) ([]int64, error)
	IsFollowing(ctx context.Context, userID, targetID int64) (bool, error)
	IsFollower(ctx context.Context, userID, targetID int64) (bool, error)
	IsBlacked(ctx context.Context, userID, targetID int64) (bool, error)
	IsFriend(ctx context.Context, userID, targetID int64) (bool, error)
}

type RelationService struct {
	relationRepo  repo.RelationRepo
	relationCache repo.RelationCacheRepo
	notifier      Notifier
	// counter 计数调整钩子（网关注入 userService.AdjustCounter，best-effort）。
	counter func(ctx context.Context, uid int64, field string, delta int64)
}

func NewRelationService(relationRepo repo.RelationRepo, relationCache repo.RelationCacheRepo, notifier Notifier) *RelationService {
	return &RelationService{
		relationRepo:  relationRepo,
		relationCache: relationCache,
		notifier:      notifier,
	}
}

// SetCounterUpdater 注入关注/粉丝计数更新器（网关用 Redis 原子计数实现）。
func (s *RelationService) SetCounterUpdater(fn func(ctx context.Context, uid int64, field string, delta int64)) {
	s.counter = fn
}

// adjustCounter 带 1s 超时的 best-effort 计数更新。
func (s *RelationService) adjustCounter(ctx context.Context, uid int64, field string, delta int64) {
	if s.counter == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s.counter(cctx, uid, field, delta)
}

// 回填缓存能力
type cacheSetter interface {
	SetFollowingIDs(ctx context.Context, userID int64, ids []int64) error
	SetFollowerIDs(ctx context.Context, userID int64, ids []int64) error
	SetBlackIDs(ctx context.Context, userID int64, ids []int64) error
	SetFriendIDs(ctx context.Context, userID int64, ids []int64) error
}

// ---------- 具体实现 ----------

func (s *RelationService) Follow(ctx context.Context, followerID, followingID int64) error {
	return s.mutateRelation(ctx, followerID, followingID, repo.RelationFollow)
}

func (s *RelationService) Unfollow(ctx context.Context, followerID, followingID int64) error {
	return s.mutateRelation(ctx, followerID, followingID, repo.RelationUnfollow)
}

func (s *RelationService) Black(ctx context.Context, userID, targetID int64) error {
	return s.mutateRelation(ctx, userID, targetID, repo.RelationBlack)
}

func (s *RelationService) Unblack(ctx context.Context, userID, targetID int64) error {
	return s.mutateRelation(ctx, userID, targetID, repo.RelationUnblack)
}

func (s *RelationService) mutateRelation(ctx context.Context, actor, target int64, action repo.RelationAction) error {
	if actor <= 0 || target <= 0 || actor == target {
		return repo.ErrInvalidRelation
	}
	transactional, ok := s.relationRepo.(repo.RelationMutationRepo)
	if !ok {
		return repo.ErrRelationTransactionUnavailable
	}
	result, err := transactional.MutateRelation(ctx, actor, target, action)
	if err != nil {
		return err
	}
	if !result.Changed {
		// The SQL state already matches the request, but the cache may lag
		// (for example after a lost invalidation). Enforce the requested
		// state so IsFollowing/follower lists cannot stay stale for the
		// whole seven-day cache TTL.
		if s.relationCache != nil {
			s.enforceRelationCache(ctx, actor, target, action)
		}
		return nil
	}
	// The transaction has committed; Redis errors cannot change its result.
	for _, d := range result.Deltas {
		s.adjustCounter(ctx, d.UserID, d.Field, d.Delta)
	}
	if s.relationCache == nil {
		return nil
	}
	s.applyRelationCache(ctx, actor, target, result.Before, result.After)
	s.applyRelationCache(ctx, target, actor, result.ReverseBefore, result.ReverseAfter)
	mutualBefore := result.Before == model.RelationStatusFollow && result.ReverseBefore == model.RelationStatusFollow
	mutualAfter := result.After == model.RelationStatusFollow && result.ReverseAfter == model.RelationStatusFollow
	if mutualAfter && !mutualBefore {
		_ = s.relationCache.AddFriend(ctx, actor, target)
		_ = s.relationCache.AddFriend(ctx, target, actor)
	} else if !mutualAfter {
		_ = s.relationCache.RemoveFriend(ctx, actor, target)
		_ = s.relationCache.RemoveFriend(ctx, target, actor)
	}
	return nil
}

func (s *RelationService) applyRelationCache(ctx context.Context, actor, target int64, before, after int8) {
	if before == after {
		return
	}
	if before == model.RelationStatusFollow {
		_ = s.relationCache.RemoveFollowing(ctx, actor, target)
		_ = s.relationCache.RemoveFollower(ctx, target, actor)
	}
	if before == model.RelationStatusBlack {
		_ = s.relationCache.RemoveBlack(ctx, actor, target)
	}
	if after == model.RelationStatusFollow {
		_ = s.relationCache.AddFollowing(ctx, actor, target)
		_ = s.relationCache.AddFollower(ctx, target, actor)
	}
	if after == model.RelationStatusBlack {
		_ = s.relationCache.AddBlack(ctx, actor, target)
	}
}

// enforceRelationCache repairs a stale positive entry after an idempotent
// removal. Readers rebuild a missing cache from SQL, but they trust a cached
// hit, so only removals need enforcing; keeps no-op follow/black writes from
// touching derived state.
func (s *RelationService) enforceRelationCache(ctx context.Context, actor, target int64, action repo.RelationAction) {
	switch action {
	case repo.RelationUnfollow:
		_ = s.relationCache.RemoveFollowing(ctx, actor, target)
		_ = s.relationCache.RemoveFollower(ctx, target, actor)
	case repo.RelationUnblack:
		_ = s.relationCache.RemoveBlack(ctx, actor, target)
	}
}

// ---------- 读方法：Cache-Aside ----------

// GetFollowingIDs adapts the cache-aside service to Feed's FollowProvider.
// Feed must also reload a missing or invalidated relationship cache from SQL.
func (s *RelationService) GetFollowingIDs(ctx context.Context, userID, offset, limit int64) ([]int64, error) {
	return s.GetFollowing(ctx, userID, offset, limit)
}

// CountFollowers returns the persisted total, independent of list pagination
// and best-effort counter caches. Only one row is requested with the SQL count.
func (s *RelationService) CountFollowers(ctx context.Context, userID int64) (int64, error) {
	total, _, err := s.relationRepo.GetFollowerList(ctx, userID, 0, 1)
	return int64(total), err
}

func (s *RelationService) CountFollowing(ctx context.Context, userID int64) (int64, error) {
	total, _, err := s.relationRepo.GetFollowingList(ctx, userID, 0, 1)
	return int64(total), err
}

func (s *RelationService) GetFollowers(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	ids, err := s.relationCache.GetFollowerIDs(ctx, userID, offset, limit)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("follower cache unavailable, falling back to SQL", "error", err)
	}
	if err == nil && ids != nil {
		return ids, nil
	}

	allIDs, err := s.loadAndCacheFollowers(ctx, userID)
	if err != nil {
		return nil, err
	}

	if offset >= int64(len(allIDs)) {
		return []int64{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(allIDs)) {
		end = int64(len(allIDs))
	}
	return allIDs[offset:end], nil
}

func (s *RelationService) GetFollowing(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	ids, err := s.relationCache.GetFollowingIDs(ctx, userID, offset, limit)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("following cache unavailable, falling back to SQL", "error", err)
	}
	if err == nil && ids != nil {
		return ids, nil
	}

	allIDs, err := s.loadAndCacheFollowing(ctx, userID)
	if err != nil {
		return nil, err
	}
	if offset >= int64(len(allIDs)) {
		return []int64{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(allIDs)) {
		end = int64(len(allIDs))
	}
	return allIDs[offset:end], nil
}

func (s *RelationService) GetBlacklist(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	ids, err := s.relationCache.GetBlackIDs(ctx, userID, offset, limit)
	if err != nil {
		return nil, err
	}
	if ids != nil {
		return ids, nil
	}

	allIDs, err := s.loadAndCacheBlacklist(ctx, userID)
	if err != nil {
		return nil, err
	}
	if offset >= int64(len(allIDs)) {
		return []int64{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(allIDs)) {
		end = int64(len(allIDs))
	}
	return allIDs[offset:end], nil
}

func (s *RelationService) GetFriends(ctx context.Context, userID int64, offset, limit int64) ([]int64, error) {
	ids, err := s.relationCache.GetFriendIDs(ctx, userID, offset, limit)
	if err != nil {
		return nil, err
	}
	if ids != nil {
		return ids, nil
	}

	allIDs, err := s.loadAndCacheFriends(ctx, userID)
	if err != nil {
		return nil, err
	}
	if offset >= int64(len(allIDs)) {
		return []int64{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(allIDs)) {
		end = int64(len(allIDs))
	}
	return allIDs[offset:end], nil
}

// ---------- 存在性检查（cache-aside） ----------

func (s *RelationService) IsFollowing(ctx context.Context, userID, targetID int64) (bool, error) {
	is, err := s.relationCache.IsFollowing(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if is {
		return true, nil
	}

	dbExists, err := s.relationRepo.IsFollowed(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if dbExists {
		_ = s.relationCache.AddFollowing(ctx, userID, targetID)
		_ = s.relationCache.AddFollower(ctx, targetID, userID)
	}
	return dbExists, nil
}

func (s *RelationService) IsFollower(ctx context.Context, userID, targetID int64) (bool, error) {
	// targetID 是否关注了 userID
	is, err := s.relationCache.IsFollower(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if is {
		return true, nil
	}

	dbExists, err := s.relationRepo.IsFollowed(ctx, targetID, userID)
	if err != nil {
		return false, err
	}
	if dbExists {
		_ = s.relationCache.AddFollower(ctx, userID, targetID)
		_ = s.relationCache.AddFollowing(ctx, targetID, userID)
	}
	return dbExists, nil
}

func (s *RelationService) IsBlacked(ctx context.Context, userID, targetID int64) (bool, error) {
	is, err := s.relationCache.IsBlacked(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if is {
		return true, nil
	}

	dbExists, err := s.relationRepo.IsBlacked(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if dbExists {
		_ = s.relationCache.AddBlack(ctx, userID, targetID)
	}
	return dbExists, nil
}

func (s *RelationService) IsFriend(ctx context.Context, userID, targetID int64) (bool, error) {
	is, err := s.relationCache.IsFriend(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if is {
		return true, nil
	}

	// 好友关系本质上是互相关注
	aFollowB, err := s.relationRepo.IsFollowed(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if !aFollowB {
		return false, nil
	}

	bFollowA, err := s.relationRepo.IsFollowed(ctx, targetID, userID)
	if err != nil {
		return false, err
	}
	if aFollowB && bFollowA {
		_ = s.relationCache.AddFriend(ctx, userID, targetID)
		_ = s.relationCache.AddFriend(ctx, targetID, userID)
		return true, nil
	}
	return false, nil
}

// ---------- 共同关注（直接走缓存，若不完整可回源计算） ----------

func (s *RelationService) CommonFollowing(ctx context.Context, userA, userB int64) ([]int64, error) {
	return s.relationCache.GetCommonFollowing(ctx, userA, userB)
}

// ---------- 私有辅助：从 DB 全量加载 + 缓存 ----------

func (s *RelationService) getAllFollowingsFromDB(ctx context.Context, userID int64) ([]int64, error) {
	var all []int64
	offset := 0
	limit := 200
	for {
		_, rels, err := s.relationRepo.GetFollowingList(ctx, userID, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(rels) == 0 {
			break
		}
		for _, r := range rels {
			all = append(all, r.FollowingID)
		}
		offset += limit
	}
	return all, nil
}

func (s *RelationService) getAllFollowersFromDB(ctx context.Context, userID int64) ([]int64, error) {
	var all []int64
	offset := 0
	limit := 200
	for {
		_, rels, err := s.relationRepo.GetFollowerList(ctx, userID, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(rels) == 0 {
			break
		}
		for _, r := range rels {
			all = append(all, r.FollowerID)
		}
		offset += limit
	}
	return all, nil
}

func (s *RelationService) getAllBlacklistFromDB(ctx context.Context, userID int64) ([]int64, error) {
	var all []int64
	offset := 0
	limit := 200
	for {
		_, rels, err := s.relationRepo.GetBlackList(ctx, userID, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(rels) == 0 {
			break
		}
		for _, r := range rels {
			all = append(all, r.FollowingID)
		}
		offset += limit
	}
	return all, nil
}

func (s *RelationService) getAllFriendsFromDB(ctx context.Context, userID int64) ([]int64, error) {
	// 好友 = userID 的 following 中，同时也 follow userID 的用户
	followings, err := s.getAllFollowingsFromDB(ctx, userID)
	if err != nil {
		return nil, err
	}

	var friends []int64
	for _, targetID := range followings {
		ok, err := s.relationRepo.IsFollowed(ctx, targetID, userID)
		if err != nil {
			return nil, err
		}
		if ok {
			friends = append(friends, targetID)
		}
	}
	return friends, nil
}

func (s *RelationService) loadAndCacheFollowing(ctx context.Context, userID int64) ([]int64, error) {
	allIDs, err := s.getAllFollowingsFromDB(ctx, userID)
	if err != nil {
		return nil, err
	}
	if setter, ok := s.relationCache.(cacheSetter); ok {
		_ = setter.SetFollowingIDs(ctx, userID, allIDs)
	}
	return allIDs, nil
}

func (s *RelationService) loadAndCacheFollowers(ctx context.Context, userID int64) ([]int64, error) {
	allIDs, err := s.getAllFollowersFromDB(ctx, userID)
	if err != nil {
		return nil, err
	}
	if setter, ok := s.relationCache.(cacheSetter); ok {
		_ = setter.SetFollowerIDs(ctx, userID, allIDs)
	}
	return allIDs, nil
}

func (s *RelationService) loadAndCacheBlacklist(ctx context.Context, userID int64) ([]int64, error) {
	allIDs, err := s.getAllBlacklistFromDB(ctx, userID)
	if err != nil {
		return nil, err
	}
	if setter, ok := s.relationCache.(cacheSetter); ok {
		_ = setter.SetBlackIDs(ctx, userID, allIDs)
	}
	return allIDs, nil
}

func (s *RelationService) loadAndCacheFriends(ctx context.Context, userID int64) ([]int64, error) {
	allIDs, err := s.getAllFriendsFromDB(ctx, userID)
	if err != nil {
		return nil, err
	}
	if setter, ok := s.relationCache.(cacheSetter); ok {
		_ = setter.SetFriendIDs(ctx, userID, allIDs)
	}
	return allIDs, nil
}
