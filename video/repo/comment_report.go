package repo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (r *CommentRepoMongo) hideReportedComment(ctx context.Context, id string) (string, bool, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return "", false, fmt.Errorf("invalid comment id: %w", err)
	}
	col, err := r.currentCommentCollection()
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	// Reporting hides the comment from public reads immediately. No video pre-read
	// is needed to prevent hidden/unknown roots from becoming visible again.
	filter := visibleRootCommentFilter("")
	delete(filter, "video_id")
	filter["_id"] = oid
	var before struct {
		VideoID string `bson:"video_id"`
		Status  string `bson:"status"`
	}
	// Return the pre-image so the caller can decrement comment_count exactly once
	// for the visible -> hidden transition (repeat reports match no document).
	err = col.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"status": "hidden"}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before)).Decode(&before)
	if err == mongo.ErrNoDocuments {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	return before.VideoID, true, nil
}

// Restore reverses a report-driven hide for the admin moderation console.
// It returns the pre-image video id only for the hidden -> active transition so
// the caller can increment comment_count exactly once.
func (r *CommentRepoMongo) Restore(ctx context.Context, id string) (string, bool, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return "", false, fmt.Errorf("invalid comment id: %w", err)
	}
	col, err := r.currentCommentCollection()
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	filter := bson.M{"_id": oid, "status": "hidden", "deleted_at": bson.M{"$exists": false}}
	var before struct {
		VideoID string `bson:"video_id"`
	}
	err = col.FindOneAndUpdate(ctx, filter, bson.M{"$set": bson.M{"status": "active"}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before)).Decode(&before)
	if err == mongo.ErrNoDocuments {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	return before.VideoID, true, nil
}
