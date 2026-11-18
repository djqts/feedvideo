package events

import (
	"errors"
	"sort"
	"strconv"
)

type WatchFact struct {
	ActorID    string
	Event      WatchEvent
	DurationMS int64
}
type WatchSummary struct {
	ActorID           string
	PlaybackSessionID string
	VideoID           string
	ContentVersion    int64
	DurationMS        int64
	WatchedMS         int64
	CoveredMS         int64
	EffectivePlay     bool
	CoverageComplete  bool
}
type watchInterval struct{ start, end int64 }
type watchGroup struct {
	summary   WatchSummary
	intervals []watchInterval
}

// AggregateWatch calculates coverage over durable v1 facts, independent of arrival order.
func AggregateWatch(facts []WatchFact) ([]WatchSummary, error) {
	groups := map[string]*watchGroup{}
	seenEvent := map[string]string{}
	seenSeq := map[string]string{}
	for _, fact := range facts {
		e, p := fact.Event, fact.Event.Payload
		if !WatchActorValid(fact.ActorID) || fact.DurationMS <= 0 || fact.DurationMS > 86400000 || e.Validate(e.OccurredAt, fact.DurationMS) != "" {
			return nil, errors.New("invalid watch fact")
		}
		hash := WatchPayloadHash(e) + ":" + strconv.FormatInt(fact.DurationMS, 10)
		eventKey := fact.ActorID + ":" + e.EventID
		seqKey := fact.ActorID + ":" + p.PlaybackSessionID + ":" + strconv.FormatInt(p.Seq, 10)
		if old, ok := seenEvent[eventKey]; ok {
			if old != hash {
				return nil, errors.New("conflicting event")
			}
			continue
		}
		if old, ok := seenSeq[seqKey]; ok {
			if old != hash {
				return nil, errors.New("conflicting sequence")
			}
			continue
		}
		seenEvent[eventKey] = hash
		seenSeq[seqKey] = hash
		key := fact.ActorID + ":" + p.PlaybackSessionID + ":" + p.VideoID + ":" + strconv.FormatInt(p.ContentVersion, 10)
		g := groups[key]
		if g == nil {
			g = &watchGroup{summary: WatchSummary{ActorID: fact.ActorID, PlaybackSessionID: p.PlaybackSessionID, VideoID: p.VideoID, ContentVersion: p.ContentVersion, DurationMS: fact.DurationMS}}
			groups[key] = g
		}
		if g.summary.DurationMS != fact.DurationMS {
			return nil, errors.New("inconsistent media duration")
		}
		if e.Type == "behavior.watch_progress" {
			g.summary.WatchedMS += p.WatchedDeltaMS
			start, end := min(p.IntervalStartMS, fact.DurationMS), min(p.IntervalEndMS, fact.DurationMS)
			if end > start {
				g.intervals = append(g.intervals, watchInterval{start, end})
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]WatchSummary, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		sort.Slice(g.intervals, func(i, j int) bool { return g.intervals[i].start < g.intervals[j].start })
		var start, end int64
		for i, interval := range g.intervals {
			if i == 0 {
				start, end = interval.start, interval.end
				continue
			}
			if interval.start <= end {
				end = max(end, interval.end)
			} else {
				g.summary.CoveredMS += end - start
				start, end = interval.start, interval.end
			}
		}
		g.summary.CoveredMS += end - start
		g.summary.CoverageComplete = g.summary.CoveredMS*10 >= g.summary.DurationMS*9
		g.summary.EffectivePlay = g.summary.WatchedMS >= 3000 || (g.summary.DurationMS < 3000 && g.summary.CoverageComplete)
		result = append(result, g.summary)
	}
	return result, nil
}
func CompletionRate(summaries []WatchSummary) *float64 {
	numerator, denominator := 0, 0
	for _, s := range summaries {
		if s.EffectivePlay {
			denominator++
			if s.CoverageComplete {
				numerator++
			}
		}
	}
	if denominator == 0 {
		return nil
	}
	rate := float64(numerator) / float64(denominator)
	return &rate
}
