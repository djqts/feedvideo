package main

import (
	"context"
	"time"
)

// The operation boundary keeps child-process cancellation testable without
// launching FFmpeg. Callers retain their existing command arguments.
func runMediaCommand(ctx context.Context, budget time.Duration, operation func(context.Context) ([]byte, error)) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	out, err := operation(runCtx)
	if runCtx.Err() != nil {
		return out, runCtx.Err()
	}
	return out, err
}
