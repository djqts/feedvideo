package model

import (
	"encoding/hex"
	"fmt"
	"math"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type MediaJob struct {
	JobID          string          `bson:"job_id" json:"-"`
	ContentVersion int64           `bson:"content_version" json:"-"`
	Status         string          `bson:"status" json:"-"`
	SourceKind     string          `bson:"source_kind" json:"-"`
	SourceBucket   string          `bson:"source_bucket" json:"-"`
	SourceObject   string          `bson:"source_object" json:"-"`
	Attempts       int             `bson:"attempts" json:"-"`
	NextAttemptAt  time.Time       `bson:"next_attempt_at" json:"-"`
	LeaseOwner     string          `bson:"lease_owner" json:"-"`
	LeaseUntil     time.Time       `bson:"lease_until" json:"-"`
	LeaseToken     int64           `bson:"lease_token" json:"-"`
	LastErrorCode  string          `bson:"last_error_code" json:"-"`
	OutputManifest []MediaArtifact `bson:"output_manifest,omitempty" json:"-"`
	CreatedAt      time.Time       `bson:"created_at" json:"-"`
	UpdatedAt      time.Time       `bson:"updated_at" json:"-"`
}

type MediaArtifact struct {
	Kind          string `bson:"kind" json:"kind"`
	Bucket        string `bson:"bucket" json:"bucket"`
	ObjectKey     string `bson:"object_key" json:"object_key"`
	Size          int64  `bson:"size" json:"size"`
	SHA256        string `bson:"sha256" json:"sha256"`
	ObjectVersion string `bson:"object_version" json:"object_version"`
}

type MediaLease struct {
	VideoID        primitive.ObjectID
	JobID          string
	ContentVersion int64
	LeaseOwner     string
	LeaseToken     int64
	LeaseUntil     time.Time
	Attempt        int
	SourceBucket   string
	SourceObject   string
	CoverURL       string
}

type MediaResult struct {
	Manifest []MediaArtifact
	Duration float64
	Width    int
	Height   int
}

// OutputPrefix isolates every attempt's objects, including attempts of the same job.
func OutputPrefix(lease *MediaLease) string {
	if lease == nil {
		return ""
	}
	return fmt.Sprintf("video/%s/version/%d/job/%s/token/%d/", lease.VideoID.Hex(), lease.ContentVersion, lease.JobID, lease.LeaseToken)
}

func ValidateMediaResult(lease *MediaLease, result *MediaResult) error {
	if lease == nil || lease.VideoID.IsZero() || lease.ContentVersion < 1 || lease.LeaseToken < 1 {
		return fmt.Errorf("invalid media lease")
	}
	id, err := uuid.Parse(lease.JobID)
	if err != nil || id.Version() != 4 || id.String() != lease.JobID {
		return fmt.Errorf("invalid media job id")
	}
	if result == nil || result.Width <= 0 || result.Height <= 0 || result.Duration <= 0 || math.IsNaN(result.Duration) || math.IsInf(result.Duration, 0) {
		return fmt.Errorf("invalid media dimensions or duration")
	}
	prefix := OutputPrefix(lease)
	seen := map[string]bool{}
	objects := map[string]bool{}
	for _, a := range result.Manifest {
		if (a.Kind != "480p" && a.Kind != "720p" && a.Kind != "cover") || seen[a.Kind] {
			return fmt.Errorf("invalid or duplicate artifact role")
		}
		seen[a.Kind] = true
		hash, err := hex.DecodeString(a.SHA256)
		if a.Size <= 0 || err != nil || len(hash) != 32 {
			return fmt.Errorf("invalid artifact size or SHA-256")
		}
		if a.Bucket == "" || a.Bucket == "." || a.Bucket == ".." || strings.IndexFunc(a.Bucket+a.ObjectKey, unicode.IsControl) >= 0 || strings.ContainsAny(a.Bucket, "/\\%?#") || strings.ContainsAny(a.ObjectKey, "\\%?#") || !strings.HasPrefix(a.ObjectKey, prefix) || path.Clean(a.ObjectKey) != a.ObjectKey || a.ObjectKey == prefix || strings.HasSuffix(a.ObjectKey, "/") {
			return fmt.Errorf("artifact outside lease output prefix")
		}
		object := a.Bucket + "/" + a.ObjectKey
		if objects[object] {
			return fmt.Errorf("duplicate artifact object")
		}
		objects[object] = true
	}
	if !seen["480p"] || !seen["720p"] || (lease.CoverURL == "" && !seen["cover"]) {
		return fmt.Errorf("missing required media artifacts")
	}
	return nil
}
