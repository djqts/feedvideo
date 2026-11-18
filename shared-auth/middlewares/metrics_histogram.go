package middlewares

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latencyBucketCount = 11

// Finite bounds in seconds; the final storage bucket covers values above 10s.
var latencyBuckets = [latencyBucketCount]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type latencySnapshot struct {
	count, sumUs int64
	buckets      [latencyBucketCount + 1]int64
}

type latencyHistogram struct {
	mu     sync.Mutex
	values latencySnapshot
}

func bucketIndex(seconds float64) int {
	for i, bound := range latencyBuckets {
		if seconds <= bound {
			return i
		}
	}
	return latencyBucketCount
}

func (h *latencyHistogram) observe(seconds float64) {
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	us := seconds * 1e6
	// float64(MaxInt64) rounds upward: reject that boundary before conversion.
	if us >= float64(math.MaxInt64) {
		return
	}
	h.record(seconds, int64(us))
}

func (h *latencyHistogram) observeDuration(elapsed time.Duration) {
	if elapsed < 0 {
		return
	}
	h.record(elapsed.Seconds(), elapsed.Microseconds())
}

func (h *latencyHistogram) record(seconds float64, us int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.values.count == math.MaxInt64 || us > math.MaxInt64-h.values.sumUs {
		return
	}
	h.values.count++
	h.values.sumUs += us
	h.values.buckets[bucketIndex(seconds)]++
}

func (h *latencyHistogram) snapshot() latencySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.values
}

// Compatibility gauges interpolate within finite buckets and saturate at 10s
// when the requested quantile lies in the unbounded overflow bucket.
func percentile(values latencySnapshot, p float64) float64 {
	if values.count == 0 {
		return 0
	}
	target := float64(values.count) * p
	cumulative, lower := float64(0), float64(0)
	for i, bound := range latencyBuckets {
		count := float64(values.buckets[i])
		if cumulative+count >= target {
			if count == 0 {
				return bound
			}
			return lower + (target-cumulative)/count*(bound-lower)
		}
		cumulative += count
		lower = bound
	}
	return latencyBuckets[latencyBucketCount-1]
}

func fmtHistogram(name string, labels map[string]string, values latencySnapshot) string {
	var output strings.Builder
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := keysParts(keys, labels)
	cumulative := int64(0)
	for index, bound := range latencyBuckets {
		cumulative += values.buckets[index]
		bucketLabels := append(append([]string(nil), parts...), fmt.Sprintf("le=%q", strconv.FormatFloat(bound, 'f', -1, 64)))
		fmt.Fprintf(&output, "%s_bucket{%s} %d\n", name, strings.Join(bucketLabels, ","), cumulative)
	}
	infLabels := append(append([]string(nil), parts...), `le="+Inf"`)
	fmt.Fprintf(&output, "%s_bucket{%s} %d\n", name, strings.Join(infLabels, ","), values.count)
	fmt.Fprintf(&output, "%s_sum{%s} %.6f\n", name, strings.Join(parts, ","), float64(values.sumUs)/1e6)
	fmt.Fprintf(&output, "%s_count{%s} %d\n", name, strings.Join(parts, ","), values.count)
	return output.String()
}
