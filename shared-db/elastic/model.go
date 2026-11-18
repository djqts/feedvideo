package elastic

import "encoding/json"

// ——— Request types ———

type Options struct {
	Index       string `json:"index"`
	Type        string `json:"type"`
	Id          string `json:"id"`
	Version     int64  `json:"version"`
	VersionType string `json:"version_type,omitempty"`
	Source      any    `json:"_source"`
	SeqNo       int64  `json:"_seq_no"`
	PrimaryTerm int64  `json:"_primary_term"`
}

// ——— Response types ———

type IndexResponse struct {
	Index       string `json:"_index"`
	Type        string `json:"_type"`
	Id          string `json:"_id"`
	Version     int64  `json:"_version"`
	Result      string `json:"result"`
	SeqNo       int64  `json:"_seq_no"`
	PrimaryTerm int64  `json:"_primary_term"`
}

type GetResponse struct {
	Index       string          `json:"_index"`
	Type        string          `json:"_type"`
	Id          string          `json:"_id"`
	Version     int64           `json:"_version"`
	SeqNo       int64           `json:"_seq_no"`
	PrimaryTerm int64           `json:"_primary_term"`
	Found       bool            `json:"found"`
	Source      json.RawMessage `json:"_source"`
}

type UpdateResponse struct {
	Index       string `json:"_index"`
	Type        string `json:"_type"`
	Id          string `json:"_id"`
	Version     int64  `json:"_version"`
	Result      string `json:"result"`
	SeqNo       int64  `json:"_seq_no"`
	PrimaryTerm int64  `json:"_primary_term"`
}

type DeleteResponse struct {
	Index       string `json:"_index"`
	Type        string `json:"_type"`
	Id          string `json:"_id"`
	Version     int64  `json:"_version"`
	Result      string `json:"result"`
	SeqNo       int64  `json:"_seq_no"`
	PrimaryTerm int64  `json:"_primary_term"`
}

// ——— Search / Aggregation ———

type SearchResponse struct {
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
	Shards   struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	Hits struct {
		Total struct {
			Value    int64  `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore *float64 `json:"max_score"`
		Hits     []Hit    `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]json.RawMessage `json:"aggregations,omitempty"`
	ScrollID     string                     `json:"_scroll_id,omitempty"`
}

type Hit struct {
	Index  string                     `json:"_index"`
	Type   string                     `json:"_type"`
	Id     string                     `json:"_id"`
	Score  *float64                   `json:"_score"`
	Source json.RawMessage            `json:"_source"`
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
}

type CountResponse struct {
	Count  int64 `json:"count"`
	Shards struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
}

// ——— Bulk ———

type BulkResponse struct {
	Took   int                   `json:"took"`
	Errors bool                  `json:"errors"`
	Items  []map[string]BulkItem `json:"items"`
}

type BulkItem struct {
	Index       string     `json:"_index"`
	Type        string     `json:"_type"`
	Id          string     `json:"_id"`
	Version     int64      `json:"_version"`
	Result      string     `json:"result"`
	SeqNo       int64      `json:"_seq_no"`
	PrimaryTerm int64      `json:"_primary_term"`
	Status      int        `json:"status"`
	Error       *BulkError `json:"error,omitempty"`
}

type BulkError struct {
	Type   string     `json:"type"`
	Reason string     `json:"reason"`
	Cause  *BulkError `json:"caused_by,omitempty"`
}

// ——— UpdateByQuery / DeleteByQuery ———

type UpdateByQueryResponse struct {
	Took             int   `json:"took"`
	TimedOut         bool  `json:"timed_out"`
	Total            int64 `json:"total"`
	Updated          int64 `json:"updated"`
	Deleted          int64 `json:"deleted"`
	Batches          int64 `json:"batches"`
	VersionConflicts int64 `json:"version_conflicts"`
	Noops            int64 `json:"noops"`
	Retries          struct {
		Bulk   int64 `json:"bulk"`
		Search int64 `json:"search"`
	} `json:"retries"`
	Failures []json.RawMessage `json:"failures"`
}

type DeleteByQueryResponse struct {
	Took             int   `json:"took"`
	TimedOut         bool  `json:"timed_out"`
	Total            int64 `json:"total"`
	Deleted          int64 `json:"deleted"`
	Batches          int64 `json:"batches"`
	VersionConflicts int64 `json:"version_conflicts"`
	Noops            int64 `json:"noops"`
	Retries          struct {
		Bulk   int64 `json:"bulk"`
		Search int64 `json:"search"`
	} `json:"retries"`
	Failures []json.RawMessage `json:"failures"`
}

// ——— Multi-get ———

type MgetResponse struct {
	Docs []GetResponse `json:"docs"`
}
