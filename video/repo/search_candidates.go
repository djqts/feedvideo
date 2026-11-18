package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"feedvideo.local/video/model"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type SearchCandidate struct {
	ID            string
	SourceVersion int64
	Score         *float64
}

type SearchCandidatePage struct {
	Candidates    []SearchCandidate
	Total         int64
	TotalRelation string
	Offset        int64
	Exhausted     bool
}

type SearchCandidateReader interface {
	SearchCandidates(context.Context, string, int64, int64) (*SearchCandidatePage, error)
	SearchTagCandidates(context.Context, []string, int64, int64) ([]*model.Video, int64, error)
}

func (r *VideoRepoMongo) SearchCandidates(ctx context.Context, keyword string, offset, limit int64) (*SearchCandidatePage, error) {
	if offset < 0 || offset >= 5000 || limit < 1 || limit > 200 {
		return nil, ErrSearchUnavailable
	}
	if r == nil || r.esClient == nil || r.esClient.Client == nil || r.esClient.Client.Client == nil {
		return nil, ErrSearchUnavailable
	}
	if limit > 5000-offset {
		limit = 5000 - offset
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchUnavailable, err)
	}
	query := map[string]any{
		"from": offset, "size": limit, "_source": []string{"source_version"},
		"query": map[string]any{"bool": map[string]any{
			"must":     []map[string]any{{"multi_match": map[string]any{"query": keyword, "fields": []string{"title^3", "description^2", "tags"}}}},
			"must_not": []map[string]any{{"term": map[string]any{"status": "deleted"}}},
		}},
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchUnavailable, err)
	}
	index := "videos"
	if r.searchProjectionEnabled {
		index = r.searchProjectionIndex
	}
	if index == "" {
		return nil, ErrSearchUnavailable
	}
	request := esapi.SearchRequest{Index: []string{index}, Body: bytes.NewReader(body)}
	response, err := request.Do(ctx, r.esClient.Client)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchUnavailable, err)
	}
	if response == nil || response.Body == nil {
		return nil, ErrSearchUnavailable
	}
	defer response.Body.Close()
	if response.IsError() {
		return nil, ErrSearchUnavailable
	}
	// Pointer total/value fields preserve the distinction between an exact zero
	// and missing/null metadata. The shared ES response type cannot express it.
	var wire *struct {
		TimedOut json.RawMessage `json:"timed_out"`
		Shards   json.RawMessage `json:"_shards"`
		Hits     *struct {
			Total *struct {
				Value    *int64 `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				ID     string          `json:"_id"`
				Score  *float64        `json:"_score"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchUnavailable, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrSearchUnavailable
	}
	if wire == nil || wire.Hits == nil || wire.Hits.Total == nil || wire.Hits.Total.Value == nil || wire.Hits.Hits == nil {
		return nil, ErrSearchUnavailable
	}
	if len(wire.TimedOut) > 0 {
		var timedOut *bool
		if err := json.Unmarshal(wire.TimedOut, &timedOut); err != nil || timedOut == nil || *timedOut {
			return nil, ErrSearchUnavailable
		}
	}
	if len(wire.Shards) > 0 {
		var shards *struct {
			Failed *int64 `json:"failed"`
		}
		if err := json.Unmarshal(wire.Shards, &shards); err != nil || shards == nil || shards.Failed == nil || *shards.Failed != 0 {
			return nil, ErrSearchUnavailable
		}
	}
	total, relation, hits := *wire.Hits.Total.Value, wire.Hits.Total.Relation, wire.Hits.Hits
	if total < 0 || (relation != "eq" && relation != "gte") || int64(len(hits)) > limit {
		return nil, ErrSearchUnavailable
	}
	if relation == "eq" {
		remaining := total - offset
		if remaining < 0 {
			remaining = 0
		}
		if remaining > limit {
			remaining = limit
		}
		if int64(len(hits)) != remaining {
			return nil, ErrSearchUnavailable
		}
	}
	page := &SearchCandidatePage{Offset: offset, Total: total, TotalRelation: relation, Candidates: make([]SearchCandidate, 0, len(hits))}
	for _, hit := range hits {
		// Every ES hit consumes a position, including invalid and duplicate IDs.
		// Consumers filter invalid IDs before calling the strict source reader.
		id := hit.ID
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			id = oid.Hex()
		}
		var version int64
		if len(hit.Source) > 0 {
			var source map[string]json.RawMessage
			if err := json.Unmarshal(hit.Source, &source); err != nil || source == nil {
				return nil, ErrSearchUnavailable
			}
			if raw, exists := source["source_version"]; exists {
				var value *int64
				if err := json.Unmarshal(raw, &value); err != nil || value == nil || *value < 0 {
					return nil, ErrSearchUnavailable
				}
				version = *value
			}
		}
		page.Candidates = append(page.Candidates, SearchCandidate{ID: id, SourceVersion: version, Score: hit.Score})
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchUnavailable, err)
	}
	positions := int64(len(hits))
	page.Exhausted = offset+positions >= 5000 || positions < limit || (page.TotalRelation == "eq" && offset+positions >= page.Total)
	return page, nil
}

func (r *VideoRepoMongo) SearchTagCandidates(ctx context.Context, tags []string, offset, limit int64) ([]*model.Video, int64, error) {
	if offset < 0 || offset >= 5000 || limit < 1 || limit > 200 || len(tags) > 50 {
		return nil, 0, ErrVideoReadInvalid
	}
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return nil, 0, ErrVideoReadUnavailable
	}
	if limit > 5000-offset {
		limit = 5000 - offset
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := dbCtx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	filter := publicHotVideoFilter()
	filter["tags"] = bson.M{"$in": tags}
	deadline, _ := dbCtx.Deadline()
	total, err := r.mdb.Col().CountDocuments(dbCtx, filter, options.Count().SetMaxTime(time.Until(deadline)))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if total < 0 {
		return nil, 0, ErrVideoReadUnavailable
	}
	cur, err := r.mdb.Col().Find(dbCtx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).SetSkip(offset).SetLimit(limit).SetBatchSize(int32(limit)).SetMaxTime(time.Until(deadline)))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	defer cur.Close(dbCtx)
	videos := make([]*model.Video, 0, limit)
	seen := make(map[primitive.ObjectID]struct{}, limit)
	for cur.Next(dbCtx) {
		var video model.Video
		if err := cur.Decode(&video); err != nil {
			return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
		}
		if video.ID.IsZero() || !model.IsPubliclyVisible(&video) || int64(len(videos)) >= limit {
			return nil, 0, ErrVideoReadUnavailable
		}
		if _, duplicate := seen[video.ID]; duplicate {
			return nil, 0, ErrVideoReadUnavailable
		}
		seen[video.ID] = struct{}{}
		videos = append(videos, &video)
	}
	if err := cur.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	if err := dbCtx.Err(); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrVideoReadUnavailable, err)
	}
	return videos, total, nil
}
