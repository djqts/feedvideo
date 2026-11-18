package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var (
	ErrCommentInvalid     = errors.New("invalid comment request")
	ErrCommentNotFound    = errors.New("comment target not found")
	ErrCommentUnavailable = errors.New("comment source temporarily unavailable")
)

func validCommentID(id string) bool {
	oid, err := primitive.ObjectIDFromHex(id)
	return err == nil && !oid.IsZero() && oid.Hex() == id
}

// Aggregation type checks reject arrays that ordinary query equality can match.
func visibleRootCommentFilter(videoID string) bson.M {
	return bson.M{"video_id": videoID, "$expr": bson.M{"$and": bson.A{
		bson.M{"$eq": bson.A{bson.M{"$type": "$video_id"}, "string"}},
		bson.M{"$or": bson.A{
			bson.M{"$eq": bson.A{bson.M{"$type": "$status"}, "missing"}},
			bson.M{"$and": bson.A{bson.M{"$eq": bson.A{bson.M{"$type": "$status"}, "string"}}, bson.M{"$in": bson.A{"$status", bson.A{"", "active", "reported"}}}}},
		}},
		bson.M{"$or": bson.A{
			bson.M{"$in": bson.A{bson.M{"$type": "$parent_id"}, bson.A{"missing", "null"}}},
			bson.M{"$and": bson.A{bson.M{"$eq": bson.A{bson.M{"$type": "$parent_id"}, "string"}}, bson.M{"$eq": bson.A{"$parent_id", ""}}}},
		}},
	}}}
}

func (r *CommentRepoMongo) currentCommentCollection() (*mongo.Collection, error) {
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, ErrCommentUnavailable
	}
	return r.mdb.Col().Clone(options.Collection().SetReadPreference(readpref.Primary()))
}

func decodeCommentSource(raw bson.Raw) (*model.Comment, error) {
	elements, err := raw.Elements()
	if err != nil {
		return nil, ErrCommentUnavailable
	}
	seen := map[string]bool{}
	for _, element := range elements {
		key, value := element.Key(), element.Value()
		if seen[key] {
			return nil, ErrCommentUnavailable
		}
		seen[key] = true
		switch key {
		case "_id":
			if value.Type != bson.TypeObjectID {
				return nil, ErrCommentUnavailable
			}
		case "video_id", "user_id", "content", "status":
			if value.Type != bson.TypeString {
				return nil, ErrCommentUnavailable
			}
		case "parent_id":
			if value.Type != bson.TypeString && value.Type != bson.TypeNull {
				return nil, ErrCommentUnavailable
			}
		}
	}
	var comment model.Comment
	if !seen["_id"] || !seen["video_id"] || bson.Unmarshal(raw, &comment) != nil || comment.ID.IsZero() || !validCommentID(comment.VideoID) {
		return nil, ErrCommentUnavailable
	}
	return &comment, nil
}

func (r *CommentRepoMongo) getCurrentCommentByID(ctx context.Context, id string) (*model.Comment, error) {
	if !validCommentID(id) {
		return nil, ErrCommentInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	col, err := r.currentCommentCollection()
	if err != nil {
		return nil, err
	}
	oid, _ := primitive.ObjectIDFromHex(id)
	deadline, _ := ctx.Deadline()
	raw, err := col.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().SetMaxTime(time.Until(deadline))).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, fmt.Errorf("%w: %w", ErrCommentNotFound, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	comment, err := decodeCommentSource(raw)
	if err != nil || comment.ID != oid {
		return nil, ErrCommentUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	return comment, nil
}

func currentVisibleReplies(raw bson.Raw, root *model.Comment) error {
	root.Replies = nil
	value, err := raw.LookupErr("replies")
	if err != nil || value.Type == bson.TypeNull {
		return nil
	}
	if value.Type != bson.TypeArray {
		return ErrCommentUnavailable
	}
	values, err := value.Array().Values()
	if err != nil {
		return ErrCommentUnavailable
	}
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return ErrCommentUnavailable
		}
		reply, err := decodeCommentSource(value.Document())
		if err != nil {
			return err
		}
		if reply.VideoID != root.VideoID || reply.ParentID != root.ID.Hex() || !model.IsVisibleComment(reply) {
			continue
		}
		reply.Replies = nil // Only the supported direct replies are exposed.
		root.Replies = append(root.Replies, *reply)
	}
	return nil
}

func (r *CommentRepoMongo) getCurrentCommentsPage(ctx context.Context, videoID string, page, size int64) ([]*model.Comment, int64, error) {
	if !validCommentID(videoID) || page < 1 || page > 100 || size < 1 || size > 50 {
		return nil, 0, ErrCommentInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	col, err := r.currentCommentCollection()
	if err != nil {
		return nil, 0, err
	}
	filter := visibleRootCommentFilter(videoID)
	deadline, _ := ctx.Deadline()
	total, err := col.CountDocuments(ctx, filter, options.Count().SetMaxTime(time.Until(deadline)))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: count comments: %w", ErrCommentUnavailable, err)
	}
	if total < 0 {
		return nil, 0, ErrCommentUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	cur, err := col.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip((page-1)*size).SetLimit(size).SetBatchSize(int32(size)).SetMaxTime(time.Until(deadline)))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	defer cur.Close(ctx)
	rows := make([]*model.Comment, 0, size)
	for cur.Next(ctx) {
		if int64(len(rows)) >= size {
			return nil, 0, ErrCommentUnavailable
		}
		comment, err := decodeCommentSource(cur.Current)
		if err != nil || comment.VideoID != videoID || comment.ParentID != "" || !model.IsVisibleComment(comment) {
			return nil, 0, ErrCommentUnavailable
		}
		if err := currentVisibleReplies(cur.Current, comment); err != nil {
			return nil, 0, err
		}
		rows = append(rows, comment)
	}
	if err := cur.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	return rows, total, nil
}
