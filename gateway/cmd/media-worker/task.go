package main

import (
	"encoding/json"
	"fmt"
)

// mediaTask 描述一次视频缩略图生成任务。
type mediaTask struct {
	VideoID string `json:"video_id"`
	Bucket  string `json:"bucket"`
	Object  string `json:"object"`
	Cover   bool   `json:"cover"`
	Attempt int    `json:"attempt"`
}

func parseTask(raw []byte) (*mediaTask, error) {
	var t mediaTask
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("parse media task: %w", err)
	}
	if t.VideoID == "" || t.Bucket == "" || t.Object == "" {
		return nil, fmt.Errorf("invalid media task: %s", string(raw))
	}
	return &t, nil
}

// sourceURL 生成 MinIO 对象可直读 URL（bucket 为 public-read）。
func sourceURL(endpoint, bucket, object string) string {
	return fmt.Sprintf("http://%s/%s/%s", endpoint, bucket, object)
}

// coverURL 生成 gateway 侧可访问的封面相对 URL。
func coverURL(bucket, object string) string {
	return fmt.Sprintf("/storage/%s/%s", bucket, object)
}
