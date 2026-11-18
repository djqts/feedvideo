package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrCommentForbidden = errors.New("comment belongs to another actor")

// CommentDeleteWriter returns the atomic original image for a changed root,
// or a current hidden image for a confirmed no-op. It never infers a commit
// from a missing update result.
type CommentDeleteWriter interface {
	DeleteCurrent(context.Context, string, string, string) (*model.Comment, bool, error)
}

func (r *CommentRepoMongo) DeleteCurrent(ctx context.Context, id, actor, videoID string) (*model.Comment, bool, error) {
	uid, err := strconv.ParseInt(actor, 10, 64)
	if !validCommentID(id) || !validCommentID(videoID) || err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != actor {
		return nil, false, ErrCommentInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	col, err := r.currentCommentCollection()
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	oid, _ := primitive.ObjectIDFromHex(id)
	filter := visibleRootCommentFilter(videoID)
	filter["_id"], filter["user_id"] = oid, actor
	expr := filter["$expr"].(bson.M)
	expr["$and"] = append(expr["$and"].(bson.A), bson.M{"$eq": bson.A{bson.M{"$type": "$user_id"}, "string"}})
	deadline, _ := ctx.Deadline()
	raw, err := col.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"status": "hidden"}},
		options.FindOneAndUpdate().SetUpsert(false).SetReturnDocument(options.Before).SetMaxTime(time.Until(deadline))).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		current, err := r.getCurrentCommentByID(ctx, id)
		if err != nil {
			return nil, false, err
		}
		owner, ownerErr := strconv.ParseInt(current.UserID, 10, 64)
		if ownerErr != nil || owner <= 0 || strconv.FormatInt(owner, 10) != current.UserID {
			return nil, false, ErrCommentUnavailable
		}
		if current.UserID != actor {
			return nil, false, ErrCommentForbidden
		}
		if current.VideoID != videoID || current.ParentID != "" {
			return nil, false, ErrCommentNotFound
		}
		if current.Status == "hidden" {
			return current, false, nil
		}
		if !model.IsVisibleComment(current) {
			return nil, false, ErrCommentNotFound
		}
		return nil, false, ErrCommentUnavailable
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	before, err := decodeCommentSource(raw)
	if err != nil || before.ID != oid || before.UserID != actor || before.VideoID != videoID || before.ParentID != "" || !model.IsVisibleComment(before) {
		return nil, false, ErrCommentUnavailable
	}
	return before, true, nil
}
