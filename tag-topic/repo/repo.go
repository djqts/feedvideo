package repo

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"feedvideo.local/shared-db/cache"
	mongorepo "feedvideo.local/shared-db/mongo"
	"feedvideo.local/tag-topic/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const hotTopicsKey = "hot_topics"

// normalizeName TrimSpace+ToLower+仅保留中文字符/字母/数字，长度 2~20，过滤空返""。
func NormalizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || utf8.RuneCountInString(s) < 2 || utf8.RuneCountInString(s) > 20 {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) < 2 {
		return ""
	}
	return out
}

type TopicRepo struct {
	mdb *mongorepo.MongoRepo[model.Topic]
	rdb *cache.RedisClient
}

func NewTopicRepo(db *mongo.Database, rdb *cache.RedisClient) *TopicRepo {
	return &TopicRepo{
		mdb: mongorepo.NewRepository[model.Topic](db, "topics"),
		rdb: rdb,
	}
}

func (r *TopicRepo) Create(ctx context.Context, name, desc string) (*model.Topic, error) {
	name = NormalizeName(name)
	if name == "" {
		return nil, fmt.Errorf("invalid topic name")
	}
	t := &model.Topic{Name: name, Description: desc}
	t.ID = primitive.NewObjectID()
	if _, err := r.mdb.InsertOne(ctx, t); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return r.FindByName(ctx, name)
		}
		return nil, err
	}
	return t, nil
}

func (r *TopicRepo) FindByName(ctx context.Context, name string) (*model.Topic, error) {
	return r.mdb.FindOne(ctx, bson.M{"name": name})
}

func (r *TopicRepo) FindByID(ctx context.Context, id string) (*model.Topic, error) {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid topic id: %w", err)
	}
	return r.mdb.FindByID(ctx, objID)
}

func (r *TopicRepo) CountVideos(ctx context.Context, name string) (int64, error) {
	return r.rdb.ZCard(ctx, hotTopicsKey) // placeholder: real impl uses video repo
}

func (r *TopicRepo) IncrHot(ctx context.Context, tag string) error {
	tag = NormalizeName(tag)
	if tag == "" {
		return nil
	}
	_, err := r.rdb.ZIncrBy(ctx, hotTopicsKey, tag, 1)
	return err
}

func (r *TopicRepo) TopHot(ctx context.Context, n int64) ([]struct{ Name string; Score float64 }, error) {
	zs, err := r.rdb.ZRevRangeWithScores(ctx, hotTopicsKey, 0, n-1)
	if err != nil {
		return nil, err
	}
	out := make([]struct{ Name string; Score float64 }, 0, len(zs))
	for _, z := range zs {
		out = append(out, struct{ Name string; Score float64 }{z.Member.(string), z.Score})
	}
	return out, nil
}

func (r *TopicRepo) EnsureIndexes(ctx context.Context) error {
	col := r.mdb.Col()
	idx := mongo.IndexModel{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)}
	if _, err := col.Indexes().CreateOne(ctx, idx); err != nil {
		slog.Warn("ensure topic index failed", "error", err)
	} else {
		slog.Info("ensured topic index", "index", "name_unique")
	}
	return nil
}
