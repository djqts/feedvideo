package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"feedvideo.local/user/model"
)

// indexWithRetry 使用指数退避重试 ES 索引，最多重试 maxRetries 次。
// 当所有尝试均失败时，返回错误供上层选择降级处理（如记录日志或推入死信队列）。
func (s *UserService) indexWithRetry(ctx context.Context, userID int64, doc *model.UserDocument) error {
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := s.search.IndexUser(ctx, userID, doc)
		if err == nil {
			return nil
		}
		log.Printf("ES index attempt %d/%d for user %d failed: %v", attempt, maxRetries, userID, err)
		if attempt == maxRetries {
			return fmt.Errorf("ES index failed after %d attempts: %w", maxRetries, err)
		}
		// 指数退避：1s -> 2s -> 4s
		backoff := time.Duration(1<<(attempt-1)) * time.Second
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return nil // 不可达
}


