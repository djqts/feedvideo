package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"feedvideo.local/moderation/model"
	"feedvideo.local/shared-db/elastic"
	userrepo "feedvideo.local/user/repo"
	videorepo "feedvideo.local/video/repo"
)

type ModerationRepo struct {
	esClient    *elastic.ESRepo
	userRepo    *userrepo.UserAuthRepo
	videoRepo   videorepo.VideoRepo
	commentRepo videorepo.CommentRepo
}

func NewModerationRepo(esClient *elastic.ESRepo, userRepo *userrepo.UserAuthRepo, videoRepo videorepo.VideoRepo, commentRepo videorepo.CommentRepo) *ModerationRepo {
	return &ModerationRepo{esClient: esClient, userRepo: userRepo, videoRepo: videoRepo, commentRepo: commentRepo}
}

func (r *ModerationRepo) ListReports(ctx context.Context, page, size int64) ([]*model.ReportItem, int64, error) {
	if r.esClient == nil {
		return nil, 0, nil
	}
	from := (page - 1) * size
	query := map[string]interface{}{
		"from": from, "size": size,
		"sort": []map[string]interface{}{{"timestamp": map[string]string{"order": "desc"}}},
	}
	body, _ := json.Marshal(query)
	resp, err := r.esClient.Search(ctx, "reports", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("es search reports: %w", err)
	}
	total := resp.Hits.Total.Value
	items := make([]*model.ReportItem, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		var src struct {
			CommentID string `json:"comment_id"`
			Reason    string `json:"reason"`
			Timestamp string `json:"timestamp"`
		}
		json.Unmarshal(hit.Source, &src)
		items = append(items, &model.ReportItem{
			ID: hit.Id, CommentID: src.CommentID, Reason: src.Reason,
			CreatedAt: src.Timestamp, Status: "pending",
		})
	}
	// 批量查 comment 补充 Content
	commentIDs := make([]string, 0, len(items))
	for _, it := range items {
		if it.CommentID != "" {
			commentIDs = append(commentIDs, it.CommentID)
		}
	}
	for _, it := range items {
		if it.CommentID != "" {
			if c, err := r.commentRepo.GetByID(ctx, it.CommentID); err == nil {
				it.Content = c.Content
				it.ReportedUserID = c.UserID
				if c.Status != "" {
					it.Status = c.Status
				}
			}
		}
	}
	return items, total, nil
}

func (r *ModerationRepo) UpdateUserStatus(ctx context.Context, uid int64, status int) error {
	return r.userRepo.UpdateStatus(ctx, uid, status)
}
