package repo

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"feedvideo.local/user/model"
)

// ===================== 接口定义 =====================

type UserRepo interface {
	CreateUser(ctx context.Context, user *model.User) error
	GetUserName(ctx context.Context, username string) (*model.User, error)
	GetUserByID(ctx context.Context, userID int64) (*model.User, error)
	UpdateStatus(ctx context.Context, userID int64, status int) error
	DeleteUser(ctx context.Context, userID int64) error
}

type RelationRepo interface {
	CreateRelation(ctx context.Context, relation *model.UserFollow) error
	IsFollowed(ctx context.Context, followerID, followingID int64) (bool, error)
	IsBlacked(ctx context.Context, followerID, followingID int64) (bool, error)
	GetFollowerList(ctx context.Context, userID int64, offset, limit int) (int, []*model.UserFollow, error)
	GetFollowingList(ctx context.Context, userID int64, offset, limit int) (int, []*model.UserFollow, error)
	GetBlackList(ctx context.Context, userID int64, offset, limit int) (int, []*model.UserFollow, error)
	UpdateRelation(ctx context.Context, relation *model.UserFollow) error
	DeletRelation(ctx context.Context, followerID, followingID int64, status int8) error
}

// ===================== User 仓库实现 =====================

type UserAuthRepo struct {
	db *gorm.DB
}

func NewUserAuthRepo(db *gorm.DB) *UserAuthRepo {
	return &UserAuthRepo{db: db}
}

func (r *UserAuthRepo) CreateUser(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *UserAuthRepo) GetUserName(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserAuthRepo) GetUserByID(ctx context.Context, userID int64) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("id = ?", userID).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserAuthRepo) UpdateStatus(ctx context.Context, userID int64, status int) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("status", status).Error
}

func (r *UserAuthRepo) DeleteUser(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Delete(&model.User{}, userID).Error
}

// ===================== 关系仓库实现 =====================

type Relation struct {
	db *gorm.DB
}

// relationOrderWhitelist 排序白名单：接口只接受枚举键，杜绝任意 SQL 片段注入 Order。
var relationOrderWhitelist = map[string]string{
	"created_at_desc": "created_at DESC",
	"created_at_asc":  "created_at ASC",
	"id_desc":         "id DESC",
}

// normalizeRelationOrder 将枚举键映射为安全排序子句；未知键回退 created_at DESC。
func normalizeRelationOrder(order string) string {
	if v, ok := relationOrderWhitelist[order]; ok {
		return v
	}
	return "created_at DESC"
}

func NewRelation(db *gorm.DB) *Relation {
	return &Relation{db: db}
}

func (r *Relation) CreateRelation(ctx context.Context, relation *model.UserFollow) error {
	return r.db.WithContext(ctx).Create(relation).Error
}

func (r *Relation) UpdateRelation(ctx context.Context, relation *model.UserFollow) error {
	return r.db.WithContext(ctx).Save(relation).Error
}

func (r *Relation) DeletRelation(ctx context.Context, followerID, followingID int64, status int8) error {
	return r.db.WithContext(ctx).Unscoped().
		Where("follower_id = ? AND following_id = ? AND status = ?", followerID, followingID, status).
		Delete(&model.UserFollow{}).Error
}

// isRelationExist 通用关系状态检查
func (r *Relation) isRelationExist(ctx context.Context, followerID, followingID int64, status int8) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserFollow{}).
		Where("follower_id = ? AND following_id = ? AND status = ?", followerID, followingID, status).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *Relation) IsFollowed(ctx context.Context, followerID, followingID int64) (bool, error) {
	return r.isRelationExist(ctx, followerID, followingID, model.RelationStatusFollow)
}

func (r *Relation) IsBlacked(ctx context.Context, followerID, followingID int64) (bool, error) {
	return r.isRelationExist(ctx, followerID, followingID, model.RelationStatusBlack)
}

// getRelationList 通用分页列表查询
func (r *Relation) getRelationList(ctx context.Context, condition func(*gorm.DB) *gorm.DB, order string, offset, limit int) (int, []*model.UserFollow, error) {
	var total int64
	baseQuery := r.db.WithContext(ctx).Model(&model.UserFollow{})

	// 先计算总数
	err := baseQuery.Session(&gorm.Session{}).Scopes(condition).Count(&total).Error
	if err != nil {
		return 0, nil, fmt.Errorf("failed to count relations: %v", err)
	}

	// 排序与分页
	baseQuery = baseQuery.Order(normalizeRelationOrder(order))
	var relations []*model.UserFollow
	err = baseQuery.Scopes(condition).Offset(offset).Limit(limit).Find(&relations).Error
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get relations: %v", err)
	}
	return int(total), relations, nil
}

func (r *Relation) GetFollowerList(ctx context.Context, userID int64, offset, limit int) (int, []*model.UserFollow, error) {
	return r.getRelationList(ctx,
		func(db *gorm.DB) *gorm.DB {
			return db.Where("following_id = ? AND status = ?", userID, model.RelationStatusFollow)
		},
		"created_at_desc", offset, limit,
	)
}

func (r *Relation) GetFollowingList(ctx context.Context, userID int64, offset, limit int) (int, []*model.UserFollow, error) {
	return r.getRelationList(ctx,
		func(db *gorm.DB) *gorm.DB {
			return db.Where("follower_id = ? AND status = ?", userID, model.RelationStatusFollow)
		},
		"created_at_desc", offset, limit,
	)
}

func (r *Relation) GetBlackList(ctx context.Context, userID int64, offset, limit int) (int, []*model.UserFollow, error) {
	return r.getRelationList(ctx,
		func(db *gorm.DB) *gorm.DB {
			return db.Where("follower_id = ? AND status = ?", userID, model.RelationStatusBlack)
		},
		"created_at_desc", offset, limit,
	)
}
