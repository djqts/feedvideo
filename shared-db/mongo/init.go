package mongo

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDB struct {
	Client *mongo.Client
	db     *mongo.Database
}

func Init(ctx context.Context, uri, dbName string, opts ...*options.ClientOptions) (*MongoDB, error) {
	clientOpts := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo.Connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo.Ping: %w", err)
	}
	db := client.Database(dbName)
	return &MongoDB{Client: client, db: db}, nil
}

func (d *MongoDB) Disconnect(ctx context.Context) error {
	return d.Client.Disconnect(ctx)
}

func (d *MongoDB) Database() *mongo.Database {
	return d.db
}

func (d *MongoDB) GetDatabase(dbName string) *mongo.Database {
	return d.Client.Database(dbName)
}

// WithTransaction 执行一个事务，callback 中的操作均使用事务 SessionContext。
// callback 返回 nil 会自动提交，返回错误则回滚。
func (d *MongoDB) WithTransaction(ctx context.Context, callback func(sessCtx mongo.SessionContext) error) error {
	session, err := d.Client.StartSession()
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(sessCtx mongo.SessionContext) (interface{}, error) {
		return nil, callback(sessCtx)
	})
	return err
}

// EnsureIndexes 根据定义的索引模型创建或更新索引。
// indexes 的键为集合名，值为该集合的索引模型列表。
func (d *MongoDB) EnsureIndexes(ctx context.Context, collectionName string, indexes map[string][]mongo.IndexModel) error {
	for colName, models := range indexes {
		if len(models) == 0 {
			continue
		}
		coll := d.db.Collection(colName)
		names, err := coll.Indexes().CreateMany(ctx, models)
		if err != nil {
			return fmt.Errorf("ensure indexes on %s: %w", colName, err)
		}
		log.Printf("Ensured indexes on %s: %v", colName, names)
	}
	return nil
}
