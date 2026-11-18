package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"feedvideo.local/user/model"
	"fmt"
	"strconv"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

type UserSearch struct {
	es    *elasticsearch.Client
	index string
}

func NewUserSearch(es *elasticsearch.Client, index string) *UserSearch {
	return &UserSearch{es: es, index: index}
}

func (r *UserSearch) IndexUser(ctx context.Context, userID int64, doc *model.UserDocument) error {
	if r == nil || r.es == nil {
		return nil // ES 未配置时静默跳过
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req := esapi.IndexRequest{
		Index:      r.index,
		DocumentID: fmt.Sprintf("%d", userID),
		Body:       bytes.NewReader(body),
		Refresh:    "true",
	}
	res, err := req.Do(ctx, r.es)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index error: %s", res.String())
	}
	return nil
}

func (r *UserSearch) SearchUsers(ctx context.Context, query string, offset, limit int) ([]*model.UserDocument, int64, error) {
	// Match username/tag prefixes, and exact numeric UIDs advertised by the UI.
	should := []interface{}{
		map[string]interface{}{"multi_match": map[string]interface{}{
			"query": query, "fields": []string{"username", "tags"}, "type": "bool_prefix",
		}},
	}
	if uid, err := strconv.ParseInt(query, 10, 64); err == nil && uid > 0 {
		should = append(should, map[string]interface{}{"term": map[string]interface{}{"user_id": uid}})
	}
	searchBody := map[string]interface{}{
		"from": offset,
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should":               should,
				"minimum_should_match": 1,
			},
		},
	}
	body, _ := json.Marshal(searchBody)
	req := esapi.SearchRequest{
		Index: []string{r.index},
		Body:  bytes.NewReader(body),
	}
	res, err := req.Do(ctx, r.es)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, 0, fmt.Errorf("search error: %s", res.String())
	}
	var result struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source model.UserDocument `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, 0, err
	}
	var users []*model.UserDocument
	for _, hit := range result.Hits.Hits {
		u := hit.Source
		users = append(users, &u)
	}
	return users, result.Hits.Total.Value, nil
}

func (r *UserSearch) DeleteUser(ctx context.Context, userID int64) error {
	req := esapi.DeleteRequest{
		Index:      r.index,
		DocumentID: fmt.Sprintf("%d", userID),
	}
	res, err := req.Do(ctx, r.es)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("delete error: %s", res.String())
	}
	return nil
}
