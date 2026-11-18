package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"feedvideo.local/shared-db/cache"
	"feedvideo.local/shared-db/elastic"
	mongorepo "feedvideo.local/shared-db/mongo"
	"feedvideo.local/video/model"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	commentLikePrefix = "comment_likes"
	commentHotListTTL = 1 * time.Hour
)

type CommentRepo interface {
	Create(ctx context.Context, comment *model.Comment) error
	GetByID(ctx context.Context, id string) (*model.Comment, error)
	GetByVideoID(ctx context.Context, videoID string, page, pageSize int64) ([]*model.Comment, int64, error)
	AddReply(ctx context.Context, parentID string, reply *model.Comment) error
	Like(ctx context.Context, commentID, userID string) error
	Unlike(ctx context.Context, commentID, userID string) error
	IsLiked(ctx context.Context, commentID, userID string) (bool, error)
	GetLikeCount(ctx context.Context, commentID string) (int64, error)
	Report(ctx context.Context, commentID, reason string) (videoID string, hidden bool, err error)
	Restore(ctx context.Context, commentID string) (videoID string, restored bool, err error)
	Delete(ctx context.Context, commentID string) error
	EnsureIndexes(ctx context.Context) error
}

type CommentRepoMongo struct {
	mdb      *mongorepo.MongoRepo[model.Comment]
	rdb      *cache.RedisClient
	esClient *elastic.ESRepo
}

func NewCommentRepoMongo(db *mongo.Database, rdb *cache.RedisClient, esClient *elastic.ESRepo) CommentRepo {
	return &CommentRepoMongo{
		mdb:      mongorepo.NewRepository[model.Comment](db, "comments"),
		rdb:      rdb,
		esClient: esClient,
	}
}

func (r *CommentRepoMongo) GetByID(ctx context.Context, id string) (*model.Comment, error) {
	return r.getCurrentCommentByID(ctx, id)
}

func (r *CommentRepoMongo) Create(ctx context.Context, comment *model.Comment) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if comment == nil {
		return ErrCommentInvalid
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	comment.ID = primitive.NewObjectID()
	comment.CreatedAt = time.Now()
	if comment.Status == "" {
		comment.Status = "active"
	}

	prepared := false
	if comment.ParentID == "" && comment.LikeCount == 0 && model.IsVisibleComment(comment) {
		comment.LegacyLikeEpoch, comment.LegacyLikeVersion = uuid.NewString(), 0
		if err := r.bootstrapCommentLikes(ctx, comment.ID.Hex(), comment.LegacyLikeEpoch, false); err == nil {
			prepared = true
		} else {
			slog.Warn("new comment likes preparation unavailable", "comment_id", comment.ID.Hex())
		}
	}
	if err := r.insertNewComment(ctx, comment); err != nil {
		return err
	}
	if prepared && ctx.Err() == nil {
		if err := r.bootstrapCommentLikes(ctx, comment.ID.Hex(), comment.LegacyLikeEpoch, true); err != nil {
			slog.Warn("committed comment likes initialization unavailable", "comment_id", comment.ID.Hex())
		}
	}

	// 如果是热门视频的评论，推入 Redis List 做热缓存
	if r.rdb != nil && r.rdb.Client != nil {
		hotListKey := fmt.Sprintf("%s:%s", hotCommentsPrefix, comment.VideoID)
		listLen, _ := r.rdb.LLen(ctx, hotListKey)
		if listLen > 0 || r.isHotVideo(ctx, comment.VideoID) {
			data, _ := json.Marshal(comment)
			_ = r.rdb.LPush(ctx, hotListKey, string(data))
			_ = r.rdb.LTrim(ctx, hotListKey, 0, hotCommentsMaxLen-1)
			_ = r.rdb.Expire(ctx, hotListKey, commentHotListTTL)
		}
	}
	return nil
}

func (r *CommentRepoMongo) GetByVideoID(ctx context.Context, videoID string, page, pageSize int64) ([]*model.Comment, int64, error) {
	return r.getCurrentCommentsPage(ctx, videoID, page, pageSize)
}

func (r *CommentRepoMongo) AddReply(ctx context.Context, parentID string, reply *model.Comment) error {
	if reply == nil || !validCommentID(parentID) || !validCommentID(reply.VideoID) || reply.ParentID != "" && reply.ParentID != parentID {
		return ErrCommentInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return ErrCommentUnavailable
	}
	parentOID, _ := primitive.ObjectIDFromHex(parentID)
	reply.ID = primitive.NewObjectID()
	reply.ParentID = parentID
	reply.CreatedAt = time.Now()
	if reply.Status == "" {
		reply.Status = "active"
	}
	if !model.IsVisibleComment(reply) {
		return ErrCommentInvalid
	}
	filter := visibleRootCommentFilter(reply.VideoID)
	filter["_id"] = parentOID
	result, err := r.mdb.UpdateOne(ctx, filter, bson.M{"$push": bson.M{"replies": reply}}, options.Update().SetUpsert(false))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	if result == nil || result.UpsertedCount != 0 {
		return ErrCommentUnavailable
	}
	if result.MatchedCount == 0 {
		return ErrCommentNotFound
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrCommentUnavailable
	}
	return nil
}

// ——— Comment Likes ———

func (r *CommentRepoMongo) Like(ctx context.Context, id, actor string) error {
	return r.setLegacyCommentLike(ctx, id, actor, true)
}
func (r *CommentRepoMongo) Unlike(ctx context.Context, id, actor string) error {
	return r.setLegacyCommentLike(ctx, id, actor, false)
}
func (r *CommentRepoMongo) IsLiked(ctx context.Context, id, actor string) (bool, error) {
	snapshot, err := r.ReadLegacyLikes(ctx, id, actor)
	if err != nil {
		return false, err
	}
	return snapshot.Active, nil
}
func (r *CommentRepoMongo) GetLikeCount(ctx context.Context, id string) (int64, error) {
	snapshot, err := r.ReadLegacyLikes(ctx, id, "")
	if err != nil {
		return 0, err
	}
	return snapshot.Count, nil
}

// Retained for old callers; it never reads old counters or deletes members.
func (r *CommentRepoMongo) syncCommentLikeToDB(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	snapshot, err := r.ReadLegacyLikes(ctx, id, "")
	if err != nil || snapshot.Version == 0 {
		return
	}
	if err := r.projectLegacyCommentLikes(ctx, snapshot); err != nil {
		slog.Warn("sync comment like to db failed", "comment_id", id, "error", err)
	}
}

// ——— Report ———

func (r *CommentRepoMongo) Report(ctx context.Context, commentID, reason string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	// 举报即隐藏：无论理由是否命中敏感词，公开列表都不再返回该评论；
	// 管理员仍可在举报列表中看到并处理。
	videoID, hidden, err := r.hideReportedComment(ctx, commentID)
	if err != nil {
		return "", false, fmt.Errorf("report comment: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}

	// 异步记录举报日志到 ES
	go r.logReportToES(context.Background(), commentID, reason)

	return videoID, hidden, nil
}

func (r *CommentRepoMongo) containsSensitiveWords(ctx context.Context, text string) bool {
	if r.esClient == nil {
		return false
	}
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"match": map[string]interface{}{
				"word": text,
			},
		},
	}
	body, _ := json.Marshal(query)
	count, err := r.esClient.Count(ctx, "sensitive_words", bytes.NewReader(body))
	if err != nil {
		slog.Warn("es sensitive check failed", "error", err)
		return false
	}
	return count > 0
}

func (r *CommentRepoMongo) logReportToES(ctx context.Context, commentID, reason string) {
	if r.esClient == nil {
		return
	}
	doc := map[string]interface{}{
		"comment_id": commentID,
		"reason":     reason,
		"timestamp":  time.Now(),
	}
	opts := elastic.Options{
		Index:  "reports",
		Type:   "_doc",
		Source: doc,
	}
	if _, err := r.esClient.Index(ctx, opts); err != nil {
		slog.Warn("log report to es failed", "comment_id", commentID, "error", err)
	}
}

// ——— Delete ———

func (r *CommentRepoMongo) Delete(ctx context.Context, commentID string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, err := r.getCurrentCommentByID(ctx, commentID)
	if err != nil {
		return err
	}
	if current.ParentID != "" {
		return ErrCommentNotFound
	}
	if current.Status == "hidden" {
		return nil
	}
	_, _, err = r.DeleteCurrent(ctx, commentID, current.UserID, current.VideoID)
	return err
}

// EnsureIndexes 评论列表查询索引：(video_id, created_at) 倒序。
func (r *CommentRepoMongo) EnsureIndexes(ctx context.Context) error {
	idx := mongo.IndexModel{
		Keys: bson.D{{Key: "video_id", Value: 1}, {Key: "created_at", Value: -1}},
	}
	if _, err := r.mdb.Col().Indexes().CreateOne(ctx, idx); err != nil {
		return fmt.Errorf("ensure comments index: %w", err)
	}
	return nil
}

// ——— helpers ———

func (r *CommentRepoMongo) isHotVideo(ctx context.Context, videoID string) bool {
	rank, err := r.rdb.ZRevRange(ctx, hotVideosKey, 0, hotVideosMax-1)
	if err != nil {
		return false
	}
	for _, id := range rank {
		if id == videoID {
			return true
		}
	}
	return false
}
