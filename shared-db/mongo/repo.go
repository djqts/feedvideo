package mongo

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Repo[T any] interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*T, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]*T, error)
	FindByID(ctx context.Context, id interface{}) (*T, error)
	InsertOne(ctx context.Context, doc *T) (*mongo.InsertOneResult, error)
	InsertMany(ctx context.Context, docs []*T) (*mongo.InsertManyResult, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	UpdateByID(ctx context.Context, id interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)
	DeleteByID(ctx context.Context, id interface{}, opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)
	Count(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	Exists(ctx context.Context, filter interface{}) (bool, error)
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, result any) (*mongo.Cursor, error)
	Page(ctx context.Context, filter any, page, pageSize int64, sort any, opts ...*options.FindOptions) (*PageResult[T], error)
}

// PageResult 分页结果
type PageResult[T any] struct {
	List       []*T  `json:"list"`
	Total      int64 `json:"total"`
	Page       int64 `json:"page"`
	Size       int64 `json:"size"`
	TotalPages int64 `json:"total_pages"`
}

type MongoRepo[T any] struct {
	col *mongo.Collection
}

func NewRepository[T any](db *mongo.Database, collectionName string) *MongoRepo[T] {
	return &MongoRepo[T]{col: db.Collection(collectionName)}
}

// Col 暴露底层 *mongo.Collection，用于高级操作
func (r *MongoRepo[T]) Col() *mongo.Collection {
	return r.col
}

func (r *MongoRepo[T]) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*T, error) {
	var result T
	err := r.col.FindOne(ctx, filter, opts...).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("document not found: %w", err)
		}
		return nil, err
	}
	return &result, nil
}

func (r *MongoRepo[T]) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]*T, error) {
	cur, err := r.col.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var results []*T
	for cur.Next(ctx) {
		var elem T
		if err := cur.Decode(&elem); err != nil {
			return nil, err
		}
		results = append(results, &elem)
	}
	return results, cur.Err()
}

func (r *MongoRepo[T]) FindByID(ctx context.Context, id interface{}) (*T, error) {
	filter := bson.M{"_id": id}
	return r.FindOne(ctx, filter)
}

func (r *MongoRepo[T]) InsertOne(ctx context.Context, doc *T) (*mongo.InsertOneResult, error) {
	if hook, ok := any(doc).(BeforeInsertHook); ok {
		if err := hook.BeforeInsert(ctx); err != nil {
			return nil, err
		}
	}

	result, err := r.col.InsertOne(ctx, doc)
	if err != nil {
		return nil, err
	}

	if hook, ok := any(doc).(AfterInsertHook); ok {
		if err := hook.AfterInsert(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *MongoRepo[T]) InsertMany(ctx context.Context, docs []*T) (*mongo.InsertManyResult, error) {
	for _, doc := range docs {
		if hook, ok := any(doc).(BeforeInsertHook); ok {
			if err := hook.BeforeInsert(ctx); err != nil {
				return nil, err
			}
		}
	}
	docss := make([]interface{}, len(docs))
	for i, doc := range docs {
		docss[i] = doc
	}
	result, err := r.col.InsertMany(ctx, docss)
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		if hook, ok := any(doc).(AfterInsertHook); ok {
			if err := hook.AfterInsert(ctx); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (r *MongoRepo[T]) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if hook, ok := any(update).(BeforeUpdateHook); ok {
		if err := hook.BeforeUpdate(ctx); err != nil {
			return nil, err
		}
	}
	result, err := r.col.UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return nil, err
	}
	if hook, ok := any(update).(AfterUpdateHook); ok {
		if err := hook.AfterUpdate(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *MongoRepo[T]) UpdateByID(ctx context.Context, id interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	filter := bson.M{"_id": id}
	return r.UpdateOne(ctx, filter, update, opts...)
}

func (r *MongoRepo[T]) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if hook, ok := any(filter).(BeforeDeleteHook); ok {
		if err := hook.BeforeDelete(ctx); err != nil {
			return nil, err
		}
	}
	result, err := r.col.DeleteOne(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	if hook, ok := any(filter).(AfterDeleteHook); ok {
		if err := hook.AfterDelete(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *MongoRepo[T]) DeleteByID(ctx context.Context, id interface{}, opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	filter := bson.M{"_id": id}
	return r.DeleteOne(ctx, filter, opts...)
}

func (r *MongoRepo[T]) Count(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return r.col.CountDocuments(ctx, filter, opts...)
}

func (r *MongoRepo[T]) Exists(ctx context.Context, filter interface{}) (bool, error) {
	count, err := r.col.CountDocuments(ctx, filter, options.Count().SetLimit(1))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *MongoRepo[T]) Aggregate(ctx context.Context, pipeline mongo.Pipeline, result any) (*mongo.Cursor, error) {
	return r.col.Aggregate(ctx, pipeline)
}

func (r *MongoRepo[T]) Page(ctx context.Context, filter any, page, pageSize int64, sort any, opts ...*options.FindOptions) (*PageResult[T], error) {
	// 计算总条数
	total, err := r.col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("count failed: %w", err)
	}

	// 计算需要跳过的文档数
	skip := (page - 1) * pageSize
	if skip < 0 {
		skip = 0
	}

	// 构造查找选项：排序、跳过、限制
	findOpts := options.Find().
		SetSort(sort).
		SetSkip(skip).
		SetLimit(pageSize)

	// 合并额外传入的选项（如有）
	for _, opt := range opts {
		// 注：options.Find() 返回的是 *options.FindOptions，opts 也是 *options.FindOptions，
		// 这里直接合并会覆盖之前 set 的值，出于简单我们直接合并，但需要注意 opts 优先级更高
		if opt != nil {
			findOpts = options.MergeFindOptions(findOpts, opt)
		}
	}

	cur, err := r.col.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("find failed: %w", err)
	}
	defer cur.Close(ctx)

	var list []*T
	for cur.Next(ctx) {
		var elem T
		if err := cur.Decode(&elem); err != nil {
			return nil, fmt.Errorf("decode failed: %w", err)
		}
		list = append(list, &elem)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	totalPages := int64(0)
	if pageSize > 0 {
		totalPages = int64(math.Ceil(float64(total) / float64(pageSize)))
	}

	return &PageResult[T]{
		List:       list,
		Total:      total,
		Page:       page,
		Size:       pageSize,
		TotalPages: totalPages,
	}, nil
}
