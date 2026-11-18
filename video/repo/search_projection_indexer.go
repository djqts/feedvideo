package repo

import (
	"context"
	config "feedvideo.local/shared-config"
	"feedvideo.local/shared-db/elastic"
	"feedvideo.local/video/model"
)

type searchProjectionIndexer struct {
	es    *elastic.ESRepo
	index string
}

func NewSearchProjectionIndexer(es *elastic.ESRepo, index string) *searchProjectionIndexer {
	return &searchProjectionIndexer{es: es, index: index}
}
func (s *searchProjectionIndexer) Index(ctx context.Context, v *model.Video) error {
	if s == nil || s.es == nil || s.es.Client == nil || s.es.Client.Client == nil {
		return ErrSearchProjectionUnavailable
	}
	index, err := config.ParseSearchProjectionIndex(s.index)
	if err != nil || v == nil || v.ID.IsZero() || v.Version <= 0 || (v.SearchProjection != nil && v.SearchProjection.SourceVersion != v.Version) {
		return ErrSearchProjectionInvalid
	}
	doc := map[string]any{"video_id": v.ID.Hex(), "source_version": v.Version, "status": v.Status}
	if v.Status == "deleted" || v.DeletedAt != nil {
		doc["status"] = "deleted"
	} else {
		// Lease/attempt metadata and timestamps unrelated to the source cannot change retries.
		doc["title"], doc["description"], doc["tags"] = v.Title, v.Description, v.Tags
		doc["author_id"], doc["location"], doc["music"] = v.AuthorID, v.Location, v.Music
		doc["cover_url"], doc["video_url"], doc["created_at"], doc["deleted_at"] = v.CoverURL, v.VideoURL, v.CreatedAt, v.DeletedAt
		doc["media_status"], doc["transcode_status"] = v.MediaStatus, v.TranscodeStatus
		doc["transcoded_url"], doc["transcoded_url_720p"] = v.TranscodedURL, v.TranscodedURL720
		doc["duration"], doc["width"], doc["height"] = v.Duration, v.Width, v.Height
	}
	_, err = s.es.Index(ctx, elastic.Options{Index: index, Id: v.ID.Hex(), Version: v.Version, VersionType: "external_gte", Source: doc})
	return err
}
