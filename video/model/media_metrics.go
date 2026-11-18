package model

import "time"

// MediaJobMetrics is a snapshot, never a source of cumulative counters.
type MediaJobMetrics struct {
	Queued, RetryWait, Processing, Succeeded, Failed int64
	Active                                           int64
	OldestCreatedAt                                  time.Time
}
