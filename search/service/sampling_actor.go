package service

import (
	"context"
	"strings"
)

type samplingActorKey struct{}
type samplingActor struct{ kind, id string }

// WithSamplingActor supplies a statistics identity, not authentication or
// authorization. Invalid input clears any inherited statistics identity.
func WithSamplingActor(ctx context.Context, kind, id string) context.Context {
	actor := samplingActor{}
	if (kind == "account" || kind == "session") && len(id) >= 1 && len(id) <= 128 {
		actor = samplingActor{kind: kind, id: strings.Clone(id)}
	}
	return context.WithValue(ctx, samplingActorKey{}, actor)
}

func samplingActorFromContext(ctx context.Context) samplingActor {
	actor, _ := ctx.Value(samplingActorKey{}).(samplingActor)
	return actor
}
