package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"feedvideo.local/notification/model"
	events "feedvideo.local/shared-events"
)

// Store 通知持久化（Mongo 支撑），可被 mock。
type Store interface {
	InsertOnce(ctx context.Context, n *model.Notification) (bool, error)
	ListByRecipient(ctx context.Context, recipient string, page, pageSize int64, typ string, unreadOnly bool) ([]*model.Notification, int64, error)
	MarkRead(ctx context.Context, recipient string, ids []string, all bool, typ string) (map[string]int64, error)
	CountUnreadByType(ctx context.Context, recipient string) (map[string]int64, error)
	EnsureIndexes(ctx context.Context) error
}

// UnreadCounter 未读计数（Redis 支撑）。
type UnreadCounter interface {
	IncrUnread(ctx context.Context, uid, typ string) error
	GetUnread(ctx context.Context, uid string) (map[string]int64, error)
	DecrUnread(ctx context.Context, uid, typ string, n int64) error
	SetAll(ctx context.Context, uid string, counts map[string]int64) error
	Invalidate(ctx context.Context, uid string) error
}

const maxSystemRecipients = 1000

var ErrUnavailable = errors.New("notification dependency unavailable")

type Service struct {
	store  Store
	unread UnreadCounter
}

func NewService(store Store, unread UnreadCounter) *Service {
	return &Service{store: store, unread: unread}
}

// UnreadCounts 未读计数聚合结果。
type UnreadCounts struct {
	ByType map[string]int64 `json:"by_type"`
	Total  int64            `json:"total"`
}

// Store 落库（Mongo-first）：Mongo 失败返回 err（worker 据此 Nack）；成功后 Redis 未读+1（失败仅 log）。
func (s *Service) Store(ctx context.Context, evt events.Event) error {
	createdAt := time.Now()
	if evt.CreatedAt > 0 {
		createdAt = time.Unix(evt.CreatedAt, 0)
	}
	n := &model.Notification{
		EventID:        evt.EventID,
		EventCreatedAt: evt.CreatedAt,
		RecipientID:    evt.RecipientID,
		Type:           string(evt.Type),
		ActorID:        evt.ActorID,
		TargetID:       evt.TargetID,
		TargetType:     evt.TargetType,
		Content:        evt.Content,
		Read:           false,
		CreatedAt:      createdAt,
	}
	inserted, err := s.store.InsertOnce(ctx, n)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if err := s.unread.IncrUnread(ctx, evt.RecipientID, string(evt.Type)); err != nil {
		slog.Warn("incr unread failed (degraded)", "uid", evt.RecipientID, "type", evt.Type, "error", err)
	}
	return nil
}

func (s *Service) List(ctx context.Context, uid string, page, pageSize int64, typ string, unreadOnly bool) ([]*model.Notification, int64, error) {
	return s.store.ListByRecipient(ctx, uid, page, pageSize, typ, unreadOnly)
}

func (s *Service) EnsureIndexes(ctx context.Context) error { return s.store.EnsureIndexes(ctx) }

func (s *Service) UnreadCount(ctx context.Context, uid string) (UnreadCounts, error) {
	byType, err := s.unread.GetUnread(ctx, uid)
	if err != nil || byType == nil {
		byType, err = s.store.CountUnreadByType(ctx, uid)
		if err != nil {
			return UnreadCounts{}, fmt.Errorf("%w: count unread: %w", ErrUnavailable, err)
		}
		if byType == nil {
			byType = map[string]int64{}
		}
		if err := s.unread.SetAll(ctx, uid, byType); err != nil {
			slog.Warn("refresh unread cache failed", "uid", uid, "error", err)
		}
	}
	var total int64
	for _, v := range byType {
		total += v
	}
	return UnreadCounts{ByType: byType, Total: total}, nil
}

// MarkRead 先 Mongo（失败返回 err）；成功后按类型递减 Redis（失败仅 log）。
func (s *Service) MarkRead(ctx context.Context, uid string, ids []string, all bool, typ string) error {
	if _, err := s.store.MarkRead(ctx, uid, ids, all, typ); err != nil {
		return fmt.Errorf("%w: mark read: %w", ErrUnavailable, err)
	}
	if err := s.unread.Invalidate(ctx, uid); err != nil {
		slog.Warn("invalidate unread cache failed", "uid", uid, "error", err)
	}
	// 自愈对账：以 Mongo 为准整体覆盖 Redis 未读，消除"统计+更新"两步窗口与负数漂移
	counts, err := s.store.CountUnreadByType(ctx, uid)
	if err != nil {
		slog.Warn("reconcile unread: count failed (degraded)", "uid", uid, "error", err)
		return fmt.Errorf("%w: reconcile unread: %w", ErrUnavailable, err)
	}
	if err := s.unread.SetAll(ctx, uid, counts); err != nil {
		slog.Warn("reconcile unread: set failed (degraded)", "uid", uid, "error", err)
	}
	return nil
}

// System 系统通知：直接落库（无 actor），recipient 上限 1000。
func (s *Service) System(ctx context.Context, recipients []string, content string) error {
	if len(recipients) > maxSystemRecipients {
		return fmt.Errorf("too many recipients: %d (max %d)", len(recipients), maxSystemRecipients)
	}
	now := time.Now()
	for _, r := range recipients {
		if r == "" {
			continue
		}
		n := &model.Notification{
			RecipientID: r,
			Type:        string(events.NotifSystem),
			Content:     content,
			Read:        false,
			CreatedAt:   now,
		}
		inserted, err := s.store.InsertOnce(ctx, n)
		if err != nil {
			return err
		}
		if !inserted {
			continue
		}
		if err := s.unread.IncrUnread(ctx, r, string(events.NotifSystem)); err != nil {
			slog.Warn("system incr unread failed (degraded)", "uid", r, "error", err)
		}
	}
	return nil
}
