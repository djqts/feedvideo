// Package outbox 实现可靠事件 Outbox（P3.2）：
//   - 业务写入与事件记录通过事务保证一致（MySQL 落地的业务路径）；
//   - 事件以 event_id 为幂等键，relay 至少一次投递，消费者按 event_id 去重；
//   - 投递失败保留 pending 并计数，可重试；DLQ 支持人工/自动重放。
package outbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Event 待投递事件。
type Event struct {
	EventType     string
	EventID       string // 幂等键
	AggregateType string
	AggregateID   string
	Payload       []byte // 序列化后的 events.Event JSON
}

// Record outbox 表记录。
type Record struct {
	ID            int64     `gorm:"primaryKey"`
	EventType     string    `gorm:"size:64;not null"`
	EventID       string    `gorm:"size:128;uniqueIndex;not null"`
	AggregateType string    `gorm:"size:32;not null"`
	AggregateID   string    `gorm:"size:64;not null"`
	Payload       string    `gorm:"type:json;not null"`
	PayloadHash   string    `gorm:"size:64;column:payload_hash"`
	Status        string    `gorm:"size:16;index;default:pending"`
	Attempts      int       `gorm:"default:0"`
	CreatedAt     time.Time `gorm:"index"`
	PublishedAt   *time.Time
	// 租约字段：claim 只在短事务内完成，发布不持数据库锁；崩溃后租约过期可重领。
	ClaimedAt  *time.Time `gorm:"column:claimed_at"`
	ClaimOwner string     `gorm:"size:128;column:claim_owner"`
	LeaseUntil *time.Time `gorm:"column:lease_until"`
	ClaimToken uint64     `gorm:"column:claim_token;not null;default:0"`
}

// TableName 固定为 outbox_events（gorm 默认会将 Record 复数化为 records，
// 与 ClaimLease 的 raw SQL 表名保持一致）。
func (Record) TableName() string {
	return "outbox_events"
}

const (
	StatusPending   = "pending"
	StatusPublished = "published"
	StatusFailed    = "failed"
)

// MaxRelayAttempts 单条事件投递失败的重试上限；达到后置为 failed，避免 outbox 表无限增长。
const MaxRelayAttempts = 10

// Store 提供 outbox_events 表的读写。
type Store struct {
	db *gorm.DB
}

// NewStore 创建 Outbox Store 并自动建表。
func NewStore(db *gorm.DB) (*Store, error) {
	if err := db.AutoMigrate(&Record{}); err != nil {
		return nil, fmt.Errorf("outbox migrate: %w", err)
	}
	return &Store{db: db}, nil
}

var ErrEventConflict = errors.New("outbox event identity conflict")
var ErrLeaseLost = errors.New("outbox lease_lost")
var ErrLeaseRequired = errors.New("outbox lease required")

// Insert owns a short transaction; business transactions should call InsertTx.
func (s *Store) Insert(ctx context.Context, evts ...Event) error {
	if len(evts) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return InsertTx(ctx, tx, evts...) })
}

// InsertTx uses the caller's transaction without committing it. A duplicate key
// succeeds only when its immutable metadata and canonical JSON are identical.
func InsertTx(ctx context.Context, tx *gorm.DB, evts ...Event) error {
	if tx == nil {
		return errors.New("outbox transaction is nil")
	}
	for _, e := range evts {
		if strings.TrimSpace(e.EventID) == "" || len(e.EventID) > 128 {
			return errors.New("outbox invalid event_id")
		}
		payload, err := canonicalJSON(e.Payload)
		if err != nil {
			return fmt.Errorf("outbox invalid payload: %w", err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(payload))
		// The no-op duplicate update acquires the existing unique-key row lock.
		if err := tx.WithContext(ctx).Exec(`INSERT INTO outbox_events
			(event_type, event_id, aggregate_type, aggregate_id, payload, payload_hash, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, NOW(3)) ON DUPLICATE KEY UPDATE event_id = event_id`,
			e.EventType, e.EventID, e.AggregateType, e.AggregateID, string(payload), hash, StatusPending).Error; err != nil {
			return err
		}
		var stored Record
		result := tx.WithContext(ctx).Raw(`SELECT * FROM outbox_events WHERE event_id = ? FOR UPDATE`, e.EventID).Scan(&stored)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("outbox inserted event not found")
		}
		original, err := canonicalJSON([]byte(stored.Payload))
		if err != nil || stored.EventID != e.EventID || stored.EventType != e.EventType || stored.AggregateType != e.AggregateType || stored.AggregateID != e.AggregateID || !bytes.Equal(original, payload) {
			return fmt.Errorf("%w: %s", ErrEventConflict, e.EventID)
		}
		// Legacy rows get a hash only after their persisted content was compared.
		if stored.PayloadHash == "" {
			if err := tx.WithContext(ctx).Exec(`UPDATE outbox_events SET payload_hash = ? WHERE id = ? AND (payload_hash IS NULL OR payload_hash = '')`, hash, stored.ID).Error; err != nil {
				return err
			}
		} else if stored.PayloadHash != hash {
			return fmt.Errorf("%w: payload hash", ErrEventConflict)
		}
	}
	return nil
}

func canonicalJSON(payload []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("expected one JSON value")
	}
	return json.Marshal(value)
}

// ClaimPending rejects unfenced claiming. Deprecated: use ClaimLease.
func (s *Store) ClaimPending(ctx context.Context, limit int) ([]Record, error) {
	return nil, ErrLeaseRequired
}

// ClaimLease 在短事务内领取待投递事件：SELECT ... FOR UPDATE SKIP LOCKED 后立即写入
// claimed_at / claim_owner / lease_until。发布过程不持锁；relay 崩溃后租约过期可被其他实例重领。
func (s *Store) ClaimLease(ctx context.Context, limit int, owner string, leaseSeconds int) ([]Record, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if owner == "" {
		owner = "relay"
	}
	if leaseSeconds <= 0 {
		leaseSeconds = 30
	}
	var rows []Record
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(
			`SELECT * FROM outbox_events
			 WHERE status = ? AND attempts < ?
			   AND (lease_until IS NULL OR lease_until <= NOW(3))
			 ORDER BY created_at ASC LIMIT ? FOR UPDATE SKIP LOCKED`,
			StatusPending, MaxRelayAttempts, limit,
		).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		if err := tx.Exec(`UPDATE outbox_events SET claim_token = claim_token + 1,
			claimed_at = NOW(3), claim_owner = ?, lease_until = TIMESTAMPADD(SECOND, ?, NOW(3))
			WHERE id IN ?`, owner, leaseSeconds, ids).Error; err != nil {
			return err
		}
		// Return the token and timestamps actually stored by the database.
		return tx.Raw(`SELECT * FROM outbox_events WHERE id IN ? ORDER BY created_at ASC, id ASC`, ids).Scan(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("outbox claim lease: %w", err)
	}
	return rows, nil
}

// ExceedsMaxAttempts 判断 attempts 是否已达到投递重试上限。
func ExceedsMaxAttempts(attempts int) bool {
	return attempts >= MaxRelayAttempts
}

// MarkPublishedLease accepts only a still-current, unexpired pending claim.
func (s *Store) MarkPublishedLease(ctx context.Context, claim Record) error {
	result := s.db.WithContext(ctx).Exec(`UPDATE outbox_events SET status = ?, published_at = NOW(3),
		claimed_at = NULL, claim_owner = '', lease_until = NULL
		WHERE id = ? AND claim_owner = ? AND claim_token = ? AND status = ? AND lease_until > NOW(3)`,
		StatusPublished, claim.ID, claim.ClaimOwner, claim.ClaimToken, StatusPending)
	return leaseResult(result)
}

// RecordFailureLease counts a failed attempt and releases ownership in one CAS.
func (s *Store) RecordFailureLease(ctx context.Context, claim Record) error {
	// MySQL evaluates assignments left-to-right; derive status before incrementing.
	result := s.db.WithContext(ctx).Exec(`UPDATE outbox_events SET
		status = CASE WHEN attempts + 1 >= ? THEN ? ELSE ? END, attempts = attempts + 1,
		claimed_at = NULL, claim_owner = '', lease_until = NULL
		WHERE id = ? AND claim_owner = ? AND claim_token = ? AND status = ? AND lease_until > NOW(3)`,
		MaxRelayAttempts, StatusFailed, StatusPending, claim.ID, claim.ClaimOwner, claim.ClaimToken, StatusPending)
	return leaseResult(result)
}

func leaseResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLeaseLost
	}
	return nil
}

// MarkPublished rejects unfenced completion. Deprecated: use MarkPublishedLease.
func (s *Store) MarkPublished(ctx context.Context, ids []int64) error {
	return ErrLeaseRequired
}

// IncrementAttempts rejects unfenced failure. Deprecated: use RecordFailureLease.
func (s *Store) IncrementAttempts(ctx context.Context, ids []int64) error {
	return ErrLeaseRequired
}

// MarkFailed rejects unfenced failure. Deprecated: use RecordFailureLease.
func (s *Store) MarkFailed(ctx context.Context, ids []int64) error {
	return ErrLeaseRequired
}

// ListFailed 列出已置为 failed 的事件（供管理端审计重放）。
func (s *Store) ListFailed(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var rows []Record
	err := s.db.WithContext(ctx).
		Where("status = ?", StatusFailed).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("outbox list failed: %w", err)
	}
	return rows, nil
}

// MarkPending is a compatibility wrapper around the failed-only replay operation.
func (s *Store) MarkPending(ctx context.Context, ids []int64) error {
	_, err := s.ReplayFailed(ctx, ids)
	return err
}

// ReplayFailed resets only failed records and returns their actual matched count.
// ClaimToken is preserved, so replay cannot revive a stale claimant.
func (s *Store) ReplayFailed(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	result := s.db.WithContext(ctx).Exec(`UPDATE outbox_events SET status = ?, attempts = 0,
		claimed_at = NULL, claim_owner = '', lease_until = NULL
		WHERE id IN ? AND status = ?`, StatusPending, ids, StatusFailed)
	return result.RowsAffected, result.Error
}

// PendingStats 返回 pending 事件数量与最老 pending 事件年龄（秒），供指标与告警。
func (s *Store) PendingStats(ctx context.Context) (count int64, oldestAgeSeconds int64, err error) {
	if err := s.db.WithContext(ctx).
		Model(&Record{}).
		Where("status = ?", StatusPending).
		Count(&count).Error; err != nil {
		return 0, 0, fmt.Errorf("outbox count pending: %w", err)
	}
	var ts []time.Time
	if err := s.db.WithContext(ctx).
		Model(&Record{}).
		Where("status = ?", StatusPending).
		Order("created_at ASC").
		Limit(1).
		Pluck("created_at", &ts).Error; err != nil {
		return 0, 0, fmt.Errorf("outbox oldest pending: %w", err)
	}
	if len(ts) > 0 && !ts[0].IsZero() {
		oldestAgeSeconds = int64(time.Since(ts[0]).Seconds())
	}
	return count, oldestAgeSeconds, nil
}

// CountPending 待投递数量（监控/告警用）。
func (s *Store) CountPending(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).
		Model(&Record{}).
		Where("status = ?", StatusPending).
		Count(&n).Error
	return n, err
}

// DecodeEvent 还原 Payload 为业务事件。
func (r Record) DecodeEvent() (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Payload), &m); err != nil {
		return nil, err
	}
	return m, nil
}
