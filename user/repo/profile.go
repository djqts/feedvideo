package repo

import (
	"context"
	"errors"

	"feedvideo.local/user/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrProfileNotFound is returned by GetProfile when no profile document exists.
var ErrProfileNotFound = errors.New("profile not found")

type UserProfile struct {
	col *mongo.Collection
}

func NewUserProfile(col *mongo.Collection) *UserProfile {
	return &UserProfile{
		col: col,
	}
}

func (r *UserProfile) GetProfile(ctx context.Context, userID int64) (*model.MongoUser, error) {
	filter := bson.M{"_id": userID}
	var user model.MongoUser
	err := r.col.FindOne(ctx, filter).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserProfile) UpdateProfile(ctx context.Context, userID int64, fields map[string]interface{}) error {
	filter := bson.M{"_id": userID}
	update := bson.M{"$set": fields}
	// Upsert: registration initializes a missing profile document; updates
	// still only touch the matched document when it exists.
	_, err := r.col.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	return err
}

func (r *UserProfile) BatchGetProfiles(ctx context.Context, userIDs []int64) ([]*model.MongoUser, error) {
	filter := bson.M{"_id": bson.M{"$in": userIDs}}
	cursor, err := r.col.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []*model.MongoUser
	if err = cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserProfile) AddInterest(ctx context.Context, userID int64, interest model.InterestTag) error {
	filter := bson.M{"_id": userID}
	update := bson.M{"$push": bson.M{"interests": interest}}
	// 可选: 限制数组长度，如 $slice
	opts := options.Update().SetUpsert(false)
	_, err := r.col.UpdateOne(ctx, filter, update, opts)
	return err
}
