package repo

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"sort"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var searchSourceFields = map[string]bool{
	"title": true, "description": true, "tags": true, "status": true, "deleted_at": true,
	"author_id": true, "location": true, "music": true, "cover_url": true, "video_url": true,
	"media_status": true, "transcode_status": true, "transcoded_url": true, "transcoded_url_720p": true,
	"duration": true, "width": true, "height": true,
}

// InitSearchProjection initializes only newly inserted video candidates. Replay
// returns the first saved document, so this never resets an existing receipt.
func InitSearchProjection(video *model.Video, now time.Time) {
	if video == nil {
		return
	}
	now = now.UTC().Truncate(time.Millisecond)
	video.Version = 1
	video.SearchProjection = &model.SearchProjection{SourceVersion: 1, Dirty: true, NextAttemptAt: now, DirtySince: now}
}

// SearchVersionFilter preserves the caller's complete CAS and rejects malformed
// or overflowing versions before Mongo can evaluate the increment expression.
func SearchVersionFilter(filter bson.M) bson.M {
	guard := bson.M{
		"$or": bson.A{
			bson.M{"version": bson.M{"$exists": false}},
			bson.M{"version": bson.M{"$type": bson.A{"int", "long"}, "$gte": int64(0), "$lt": int64(math.MaxInt64)}},
		},
		// Query $type also matches array elements; aggregation $type checks the
		// field itself. Keep the missing legacy version and the indexed ranges.
		"$expr": bson.M{"$in": bson.A{bson.M{"$type": "$version"}, bson.A{"missing", "int", "long"}}},
	}
	result := maps.Clone(filter)
	if result == nil {
		result = bson.M{}
	}
	if existing, ok := result["$and"]; ok {
		result["$and"] = bson.A{bson.M{"$and": existing}, guard}
	} else {
		result["$and"] = bson.A{guard}
	}
	return result
}

func SearchSourcePipeline(set bson.M, unset bson.M, now time.Time) mongo.Pipeline {
	return searchSourcePipeline(set, unset, nil, now)
}

// expressions is reserved for internal computations, never caller-supplied values.
func searchSourcePipeline(set, unset, expressions bson.M, now time.Time) mongo.Pipeline {
	now = now.UTC().Truncate(time.Millisecond)
	literalSet := bson.M{}
	comparisons := bson.A{}
	keys := make([]string, 0, len(set)+len(expressions))
	for key := range set {
		keys = append(keys, key)
	}
	for key := range expressions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, internal := expressions[key]
		if !internal {
			value = bson.M{"$literal": set[key]}
		}
		literalSet[key] = value
		if searchSourceFields[key] {
			comparisons = append(comparisons, bson.M{"$ne": bson.A{bson.M{"$ifNull": bson.A{"$" + key, nil}}, value}})
		}
	}
	unsetKeys := make([]string, 0, len(unset))
	for key := range unset {
		unsetKeys = append(unsetKeys, key)
	}
	sort.Strings(unsetKeys)
	for _, key := range unsetKeys {
		if searchSourceFields[key] {
			comparisons = append(comparisons, bson.M{"$ne": bson.A{bson.M{"$type": "$" + key}, "missing"}})
		}
	}
	changed := any(false)
	if len(comparisons) > 0 {
		changed = bson.M{"$or": comparisons}
	}
	base := bson.M{"$cond": bson.A{bson.M{"$gt": bson.A{"$version", int64(0)}}, "$version", int64(1)}}
	next := bson.M{"$cond": bson.A{changed, bson.M{"$add": bson.A{base, int64(1)}}, base}}
	object := bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection"}, "object"}}
	refresh := bson.M{"$or": bson.A{changed, bson.M{"$ne": bson.A{bson.M{"$type": "$search_projection"}, "object"}}}}
	prior := bson.M{"$cond": bson.A{object, "$search_projection", bson.M{}}}
	dirtySince := bson.M{"$cond": bson.A{bson.M{"$and": bson.A{bson.M{"$eq": bson.A{"$search_projection.dirty", true}}, bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.dirty_since"}, "date"}}}}, "$search_projection.dirty_since", now}}
	intent := bson.M{"$mergeObjects": bson.A{prior, bson.M{
		"source_version": next, "applied_version": bson.M{"$ifNull": bson.A{"$search_projection.applied_version", int64(0)}},
		"lease_token": bson.M{"$ifNull": bson.A{"$search_projection.lease_token", int64(0)}},
		"dirty":       true, "dirty_since": dirtySince, "next_attempt_at": now, "attempts": int64(0),
		"last_error_code": "", "lease_owner": "", "lease_until": now,
	}}}
	pipeline := mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{"version": next, "search_projection": bson.M{"$cond": bson.A{refresh, intent, "$search_projection"}}}}}}
	if len(literalSet) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$set", Value: literalSet}})
	}
	if len(unsetKeys) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$unset", Value: unsetKeys}})
	}
	return pipeline
}

var searchProjectionIndexPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ConfigureSearchProjection is called only during process startup.
func (r *VideoRepoMongo) ConfigureSearchProjection(enabled bool, index string) error {
	if r == nil {
		return fmt.Errorf("nil video repository")
	}
	if enabled {
		if index == "" {
			index = "videos_projection_v1"
		}
		if index == "videos" || len(index) > 255 || !searchProjectionIndexPattern.MatchString(index) {
			return fmt.Errorf("invalid search projection index")
		}
		r.searchProjectionEnabled, r.searchProjectionIndex = true, index
	} else {
		r.searchProjectionEnabled, r.searchProjectionIndex = false, "videos"
	}
	return nil
}
