package model

// StatsSyncReceipt is internal recovery evidence, not a public API response.
// Fields retain the exact Redis strings used for the confirmed Mongo write.
type StatsSyncReceipt struct {
	VideoID    string
	Fields     map[string]string
	DirtyScore string
	Token      string
	Complete   bool
	Persisted  bool
}

type StatsSyncOutcome struct {
	VideoID string
	Receipt *StatsSyncReceipt
	Err     error
}
