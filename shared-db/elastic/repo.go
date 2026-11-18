package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

type Repository interface {
	// Index management
	Create(ctx context.Context, opts Options) (*IndexResponse, error)
	DeleteIndex(ctx context.Context, index string) (*IndexResponse, error)
	IndexExists(ctx context.Context, index string) (bool, error)
	Refresh(ctx context.Context, index string) error

	// Document CRUD
	Index(ctx context.Context, opts Options) (*IndexResponse, error)
	Get(ctx context.Context, opts Options) (*GetResponse, error)
	GetSource(ctx context.Context, opts Options) (json.RawMessage, error)
	Exists(ctx context.Context, opts Options) (bool, error)
	Update(ctx context.Context, opts Options, doc any) (*UpdateResponse, error)
	UpdateByQuery(ctx context.Context, index string, body io.Reader) (*UpdateByQueryResponse, error)
	Delete(ctx context.Context, opts Options) (*DeleteResponse, error)
	DeleteByQuery(ctx context.Context, index string, body io.Reader) (*DeleteByQueryResponse, error)

	// Search & Aggregation
	Search(ctx context.Context, index string, body io.Reader) (*SearchResponse, error)
	Count(ctx context.Context, index string, body io.Reader) (int64, error)
	Scroll(ctx context.Context, scrollID string, ttl time.Duration) (*SearchResponse, error)
	ClearScroll(ctx context.Context, scrollID string) error

	// Bulk
	Bulk(ctx context.Context, body io.Reader) (*BulkResponse, error)
	BulkIndex(ctx context.Context, index string, docs []any) (*BulkResponse, error)

	// Multi-get
	Mget(ctx context.Context, index string, ids []string) (*MgetResponse, error)
}

type ESRepo struct {
	Client *ElasticClient
}

var (
	ErrVersionConflict = errors.New("elasticsearch version conflict")
	ErrInvalidVersion  = errors.New("invalid Elasticsearch version options")
	ErrUnavailable     = errors.New("elasticsearch unavailable")
	ErrAcknowledgement = errors.New("elasticsearch acknowledgement does not match source")
)

func NewESRepo(client *ElasticClient) *ESRepo {
	return &ESRepo{Client: client}
}

// ——— Index management ———
func (r *ESRepo) Create(ctx context.Context, opts Options) (*IndexResponse, error) {
	req := esapi.IndicesCreateRequest{
		Index: opts.Index,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("create index: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("create index error [%s]: %s", res.Status(), string(body))
	}
	var ackResp struct {
		Acknowledged       bool   `json:"acknowledged"`
		ShardsAcknowledged bool   `json:"shards_acknowledged"`
		Index              string `json:"index"`
	}
	if err := json.NewDecoder(res.Body).Decode(&ackResp); err != nil {
		return nil, fmt.Errorf("decode create response: %w", err)
	}
	return &IndexResponse{Index: ackResp.Index, Result: "created"}, nil
}

func (r *ESRepo) DeleteIndex(ctx context.Context, index string) (*IndexResponse, error) {
	req := esapi.IndicesDeleteRequest{
		Index: []string{index},
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("delete index: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("delete index error [%s]: %s", res.Status(), string(body))
	}
	var ackResp struct {
		Acknowledged bool `json:"acknowledged"`
	}
	if err := json.NewDecoder(res.Body).Decode(&ackResp); err != nil {
		return nil, fmt.Errorf("decode delete index response: %w", err)
	}
	return &IndexResponse{Index: index, Result: "deleted"}, nil
}

func (r *ESRepo) IndexExists(ctx context.Context, index string) (bool, error) {
	req := esapi.IndicesExistsRequest{
		Index: []string{index},
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return false, fmt.Errorf("index exists: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		body, _ := io.ReadAll(res.Body)
		return false, fmt.Errorf("index exists error [%s]: %s", res.Status(), string(body))
	}
}

func (r *ESRepo) Refresh(ctx context.Context, index string) error {
	req := esapi.IndicesRefreshRequest{
		Index: []string{index},
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("refresh error [%s]: %s", res.Status(), string(body))
	}
	return nil
}

// ——— Document CRUD ———

func (r *ESRepo) Index(ctx context.Context, opts Options) (*IndexResponse, error) {
	if r == nil || r.Client == nil || r.Client.Client == nil {
		return nil, ErrUnavailable
	}
	if opts.Version < 0 || (opts.VersionType != "" && opts.VersionType != "external_gte") ||
		(opts.VersionType != "" && opts.Version <= 0) || (opts.Version > 0 && opts.Id == "") {
		return nil, ErrInvalidVersion
	}
	var sourceBytes []byte
	var err error
	switch v := opts.Source.(type) {
	case []byte:
		sourceBytes = v
	case string:
		sourceBytes = []byte(v)
	default:
		sourceBytes, err = json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal source: %w", err)
		}
	}

	req := esapi.IndexRequest{
		Index:       opts.Index,
		DocumentID:  opts.Id,
		Body:        bytes.NewReader(sourceBytes),
		Refresh:     "true",
		VersionType: opts.VersionType,
	}
	if opts.Version > 0 {
		version := int(opts.Version)
		if int64(version) != opts.Version {
			return nil, ErrInvalidVersion
		}
		req.Version = &version
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		return nil, ErrVersionConflict
	}
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("%w: index document error [%s]: %s", ErrUnavailable, res.Status(), string(body))
	}

	var idxResp IndexResponse
	if err := json.NewDecoder(res.Body).Decode(&idxResp); err != nil {
		return nil, fmt.Errorf("decode index response: %w", err)
	}
	if opts.Version > 0 && (idxResp.Id != opts.Id || idxResp.Version != opts.Version) {
		return nil, ErrAcknowledgement
	}
	return &idxResp, nil
}

func (r *ESRepo) Get(ctx context.Context, opts Options) (*GetResponse, error) {
	req := esapi.GetRequest{
		Index:      opts.Index,
		DocumentID: opts.Id,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("get error [%s]: %s", res.Status(), string(body))
	}
	var getResp GetResponse
	if err := json.NewDecoder(res.Body).Decode(&getResp); err != nil {
		return nil, fmt.Errorf("decode get response: %w", err)
	}
	return &getResp, nil
}

func (r *ESRepo) GetSource(ctx context.Context, opts Options) (json.RawMessage, error) {
	req := esapi.GetSourceRequest{
		Index:      opts.Index,
		DocumentID: opts.Id,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("get source: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("get source error [%s]: %s", res.Status(), string(body))
	}
	return io.ReadAll(res.Body)
}

func (r *ESRepo) Exists(ctx context.Context, opts Options) (bool, error) {
	req := esapi.ExistsRequest{
		Index:      opts.Index,
		DocumentID: opts.Id,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return false, fmt.Errorf("exists: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		body, _ := io.ReadAll(res.Body)
		return false, fmt.Errorf("exists error [%s]: %s", res.Status(), string(body))
	}
}

func (r *ESRepo) Update(ctx context.Context, opts Options, doc any) (*UpdateResponse, error) {
	bodyBytes, err := json.Marshal(map[string]any{"doc": doc})
	if err != nil {
		return nil, fmt.Errorf("marshal update body: %w", err)
	}
	req := esapi.UpdateRequest{
		Index:      opts.Index,
		DocumentID: opts.Id,
		Body:       bytes.NewReader(bodyBytes),
		Refresh:    "true",
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("update error [%s]: %s", res.Status(), string(body))
	}
	var updateResp UpdateResponse
	if err := json.NewDecoder(res.Body).Decode(&updateResp); err != nil {
		return nil, fmt.Errorf("decode update response: %w", err)
	}
	return &updateResp, nil
}

func (r *ESRepo) UpdateByQuery(ctx context.Context, index string, body io.Reader) (*UpdateByQueryResponse, error) {
	req := esapi.UpdateByQueryRequest{
		Index: []string{index},
		Body:  body,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("update by query: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("update by query error [%s]: %s", res.Status(), string(errBody))
	}
	var resp UpdateByQueryResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode update by query response: %w", err)
	}
	return &resp, nil
}

func (r *ESRepo) Delete(ctx context.Context, opts Options) (*DeleteResponse, error) {
	req := esapi.DeleteRequest{
		Index:      opts.Index,
		DocumentID: opts.Id,
		Refresh:    "true",
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("delete error [%s]: %s", res.Status(), string(body))
	}
	var deleteResp DeleteResponse
	if err := json.NewDecoder(res.Body).Decode(&deleteResp); err != nil {
		return nil, fmt.Errorf("decode delete response: %w", err)
	}
	return &deleteResp, nil
}

func (r *ESRepo) DeleteByQuery(ctx context.Context, index string, body io.Reader) (*DeleteByQueryResponse, error) {
	req := esapi.DeleteByQueryRequest{
		Index: []string{index},
		Body:  body,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("delete by query: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("delete by query error [%s]: %s", res.Status(), string(errBody))
	}
	var resp DeleteByQueryResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode delete by query response: %w", err)
	}
	return &resp, nil
}

// ——— Search & Aggregation ———

func (r *ESRepo) Search(ctx context.Context, index string, body io.Reader) (*SearchResponse, error) {
	req := esapi.SearchRequest{
		Index: []string{index},
		Body:  body,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("search error [%s]: %s", res.Status(), string(errBody))
	}
	var searchResp SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	return &searchResp, nil
}

func (r *ESRepo) Count(ctx context.Context, index string, body io.Reader) (int64, error) {
	req := esapi.CountRequest{
		Index: []string{index},
		Body:  body,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return 0, fmt.Errorf("count error [%s]: %s", res.Status(), string(errBody))
	}
	var countResp CountResponse
	if err := json.NewDecoder(res.Body).Decode(&countResp); err != nil {
		return 0, fmt.Errorf("decode count response: %w", err)
	}
	return countResp.Count, nil
}

func (r *ESRepo) Scroll(ctx context.Context, scrollID string, ttl time.Duration) (*SearchResponse, error) {
	req := esapi.ScrollRequest{
		ScrollID: scrollID,
		Scroll:   ttl,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("scroll: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("scroll error [%s]: %s", res.Status(), string(errBody))
	}
	var searchResp SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decode scroll response: %w", err)
	}
	return &searchResp, nil
}

func (r *ESRepo) ClearScroll(ctx context.Context, scrollID string) error {
	req := esapi.ClearScrollRequest{
		ScrollID: []string{scrollID},
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("clear scroll: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("clear scroll error [%s]: %s", res.Status(), string(body))
	}
	return nil
}

// ——— Bulk ———

func (r *ESRepo) Bulk(ctx context.Context, body io.Reader) (*BulkResponse, error) {
	req := esapi.BulkRequest{
		Body: body,
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("bulk: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("bulk error [%s]: %s", res.Status(), string(errBody))
	}
	var bulkResp BulkResponse
	if err := json.NewDecoder(res.Body).Decode(&bulkResp); err != nil {
		return nil, fmt.Errorf("decode bulk response: %w", err)
	}
	if bulkResp.Errors {
		// collect first error detail for diagnosis
		for _, item := range bulkResp.Items {
			for action, detail := range item {
				if detail.Error != nil {
					return &bulkResp, fmt.Errorf("bulk %s error on doc [%s]: %s: %s",
						action, detail.Id, detail.Error.Type, detail.Error.Reason)
				}
			}
		}
	}
	return &bulkResp, nil
}

func (r *ESRepo) BulkIndex(ctx context.Context, index string, docs []any) (*BulkResponse, error) {
	var buf bytes.Buffer
	for _, doc := range docs {
		action := map[string]any{
			"index": map[string]any{
				"_index": index,
			},
		}
		actionBytes, err := json.Marshal(action)
		if err != nil {
			return nil, fmt.Errorf("marshal bulk action: %w", err)
		}
		buf.Write(actionBytes)
		buf.WriteByte('\n')

		docBytes, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("marshal bulk doc: %w", err)
		}
		buf.Write(docBytes)
		buf.WriteByte('\n')
	}
	return r.Bulk(ctx, &buf)
}

// ——— Multi-get ———

func (r *ESRepo) Mget(ctx context.Context, index string, ids []string) (*MgetResponse, error) {
	docs := make([]map[string]string, len(ids))
	for i, id := range ids {
		docs[i] = map[string]string{"_id": id}
	}
	bodyBytes, err := json.Marshal(map[string]any{"docs": docs})
	if err != nil {
		return nil, fmt.Errorf("marshal mget body: %w", err)
	}
	req := esapi.MgetRequest{
		Index: index,
		Body:  bytes.NewReader(bodyBytes),
	}
	res, err := req.Do(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("mget: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		errBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("mget error [%s]: %s", res.Status(), string(errBody))
	}
	var mgetResp MgetResponse
	if err := json.NewDecoder(res.Body).Decode(&mgetResp); err != nil {
		return nil, fmt.Errorf("decode mget response: %w", err)
	}
	return &mgetResp, nil
}
