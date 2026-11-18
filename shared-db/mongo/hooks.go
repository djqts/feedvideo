package mongo

import "context"

type BeforeInsertHook interface {
	BeforeInsert(ctx context.Context) error
}

type AfterInsertHook interface {
	AfterInsert(ctx context.Context) error
}

type BeforeUpdateHook interface {
	BeforeUpdate(ctx context.Context) error
}

type AfterUpdateHook interface {
	AfterUpdate(ctx context.Context) error
}

type BeforeDeleteHook interface {
	BeforeDelete(ctx context.Context) error
}

type AfterDeleteHook interface {
	AfterDelete(ctx context.Context) error
}
