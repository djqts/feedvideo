package repo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SetVerifiedExisting never initializes profiles. Registration keeps its
// separate UpdateProfile upsert; an admin action needs a real matched document.
func (r *UserProfile) SetVerifiedExisting(ctx context.Context, uid int64, verified bool) error {
	if uid <= 0 {
		return fmt.Errorf("invalid verification target")
	}
	if ctx == nil || r == nil || r.col == nil {
		return fmt.Errorf("verification profile unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	filter := bson.M{"_id": uid, "$expr": bson.M{"$in": bson.A{bson.M{"$type": "$_id"}, bson.A{"int", "long"}}}}
	result, err := r.col.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"verified": verified}}, options.Update().SetUpsert(false))
	if err != nil {
		return fmt.Errorf("update existing verification: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if result == nil || result.MatchedCount != 1 || result.UpsertedCount != 0 {
		return ErrProfileNotFound
	}
	return nil
}
