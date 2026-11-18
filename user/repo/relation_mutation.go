package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"feedvideo.local/shared-db/outbox"
	events "feedvideo.local/shared-events"
	"feedvideo.local/user/model"
	"gorm.io/gorm"
)

type RelationAction string

const (
	RelationFollow   RelationAction = "follow"
	RelationUnfollow RelationAction = "unfollow"
	RelationBlack    RelationAction = "black"
	RelationUnblack  RelationAction = "unblack"
)

type RelationCounterDelta struct {
	UserID int64
	Field  string
	Delta  int64
}

// RelationMutation is returned only after the SQL transaction commits.
type RelationMutation struct {
	Changed                     bool
	Version                     int64
	Before, After               int8
	ReverseBefore, ReverseAfter int8
	Deltas                      []RelationCounterDelta
}

type RelationMutationRepo interface {
	MutateRelation(context.Context, int64, int64, RelationAction) (RelationMutation, error)
}

var ErrRelationTransactionUnavailable = errors.New("relation transaction capability unavailable")
var ErrRelationUnavailable = errors.New("relation service unavailable")
var ErrInvalidRelation = errors.New("invalid relation")
var ErrRelationBlocked = errors.New("cannot follow a blocked user")
var ErrRelationNotFound = errors.New("relation user not found")

func (r *Relation) MutateRelation(ctx context.Context, actorID, targetID int64, action RelationAction) (RelationMutation, error) {
	if r == nil || r.db == nil {
		return RelationMutation{}, ErrRelationTransactionUnavailable
	}
	if actorID <= 0 || targetID <= 0 || actorID == targetID {
		return RelationMutation{}, ErrInvalidRelation
	}
	if action != RelationFollow && action != RelationUnfollow && action != RelationBlack && action != RelationUnblack {
		return RelationMutation{}, ErrInvalidRelation
	}
	low, high := actorID, targetID
	if low > high {
		low, high = high, low
	}
	var result RelationMutation
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		usersTable, err := relationTableName(tx, &model.User{})
		if err != nil {
			return err
		}
		relationsTable, err := relationTableName(tx, &model.UserFollow{})
		if err != nil {
			return err
		}
		// Existing account rows serialize even pairs with no relation or version row.
		for _, id := range []int64{low, high} {
			var lockedID int64
			q := tx.Raw("SELECT id FROM "+usersTable+" WHERE id = ? AND deleted_at IS NULL FOR UPDATE", id).Scan(&lockedID)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 1 {
				return fmt.Errorf("%w: %w", ErrRelationNotFound, gorm.ErrRecordNotFound)
			}
		}
		var relations []model.UserFollow
		if err := tx.Raw("SELECT follower_id, following_id, status FROM "+relationsTable+" WHERE ((follower_id = ? AND following_id = ?) OR (follower_id = ? AND following_id = ?)) AND deleted_at IS NULL FOR UPDATE", actorID, targetID, targetID, actorID).Scan(&relations).Error; err != nil {
			return err
		}
		for _, rel := range relations {
			if rel.Status != model.RelationStatusFollow && rel.Status != model.RelationStatusBlack {
				return errors.New("invalid persisted relation status")
			}
			if rel.FollowerID == actorID {
				result.Before = rel.Status
			} else {
				result.ReverseBefore = rel.Status
			}
		}
		result.After, result.ReverseAfter = result.Before, result.ReverseBefore
		switch action {
		case RelationFollow:
			if result.Before == model.RelationStatusBlack || result.ReverseBefore == model.RelationStatusBlack {
				return ErrRelationBlocked
			}
			result.After = model.RelationStatusFollow
		case RelationUnfollow:
			if result.Before == model.RelationStatusFollow {
				result.After = model.RelationStatusNone
			}
		case RelationBlack:
			result.After = model.RelationStatusBlack
			if result.ReverseBefore == model.RelationStatusFollow {
				result.ReverseAfter = model.RelationStatusNone
			}
		case RelationUnblack:
			if result.Before == model.RelationStatusBlack {
				result.After = model.RelationStatusNone
			}
		}
		result.Changed = result.Before != result.After || result.ReverseBefore != result.ReverseAfter
		if !result.Changed {
			return nil
		}
		now := time.Now().UTC()
		// Delete both follows before adding the block; every error aborts the transaction.
		if result.Before != result.After && result.Before != model.RelationStatusNone {
			if err := deleteRelationTx(tx, relationsTable, actorID, targetID, result.Before); err != nil {
				return err
			}
		}
		if result.ReverseBefore != result.ReverseAfter {
			if err := deleteRelationTx(tx, relationsTable, targetID, actorID, result.ReverseBefore); err != nil {
				return err
			}
		}
		if result.Before != result.After && result.After != model.RelationStatusNone {
			if err := tx.Exec("INSERT INTO "+relationsTable+" (follower_id, following_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", actorID, targetID, result.After, now, now).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("INSERT INTO user_relation_versions (low_id, high_id, version, updated_at) VALUES (?, ?, 0, ?) ON DUPLICATE KEY UPDATE low_id = low_id", low, high, now).Error; err != nil {
			return err
		}
		var version int64
		q := tx.Raw("SELECT version FROM user_relation_versions WHERE low_id = ? AND high_id = ? FOR UPDATE", low, high).Scan(&version)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errors.New("relation version missing")
		}
		if version < 0 || version == math.MaxInt64 {
			return errors.New("relation version exhausted")
		}
		result.Version = version + 1
		q = tx.Exec("UPDATE user_relation_versions SET version = ?, updated_at = ? WHERE low_id = ? AND high_id = ?", result.Version, now, low, high)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errors.New("relation version update lost")
		}
		evt := events.RelationChangedEvent{SchemaVersion: 1, EventID: fmt.Sprintf("relation:%d:%d:%d", low, high, result.Version), Type: events.RelationChangedType, OccurredAt: now, Payload: events.RelationChangedPayload{ActorID: fmt.Sprint(actorID), TargetID: fmt.Sprint(targetID), Version: result.Version, Before: relationState(result.Before), After: relationState(result.After), ReverseBefore: relationState(result.ReverseBefore), ReverseAfter: relationState(result.ReverseAfter)}}
		if err := evt.Validate(); err != nil {
			return err
		}
		payload, err := json.Marshal(evt)
		if err != nil {
			return err
		}
		if err := outbox.InsertTx(ctx, tx, outbox.Event{EventType: evt.Type, EventID: evt.EventID, AggregateType: "relation", AggregateID: fmt.Sprintf("%d:%d", low, high), Payload: payload}); err != nil {
			return err
		}
		result.Deltas = appendRelationDeltas(result.Deltas, actorID, targetID, result.Before, result.After)
		result.Deltas = appendRelationDeltas(result.Deltas, targetID, actorID, result.ReverseBefore, result.ReverseAfter)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrRelationBlocked) || errors.Is(err, ErrRelationNotFound) || errors.Is(err, ErrInvalidRelation) {
			return RelationMutation{}, err
		}
		return RelationMutation{}, fmt.Errorf("%w: %w", ErrRelationUnavailable, err)
	}
	return result, nil
}

func relationTableName(tx *gorm.DB, value any) (string, error) {
	statement := &gorm.Statement{DB: tx}
	if err := statement.Parse(value); err != nil {
		return "", err
	}
	return statement.Quote(statement.Schema.Table), nil
}

func deleteRelationTx(tx *gorm.DB, table string, actor, target int64, status int8) error {
	q := tx.Exec("DELETE FROM "+table+" WHERE follower_id = ? AND following_id = ? AND status = ?", actor, target, status)
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected != 1 {
		return errors.New("relation deletion lost")
	}
	return nil
}

func relationState(status int8) string {
	switch status {
	case model.RelationStatusFollow:
		return "follow"
	case model.RelationStatusBlack:
		return "black"
	default:
		return "none"
	}
}

func appendRelationDeltas(deltas []RelationCounterDelta, actor, target int64, before, after int8) []RelationCounterDelta {
	var delta int64
	if before == model.RelationStatusFollow && after != model.RelationStatusFollow {
		delta = -1
	}
	if before != model.RelationStatusFollow && after == model.RelationStatusFollow {
		delta = 1
	}
	if delta != 0 {
		deltas = append(deltas, RelationCounterDelta{actor, "following_count", delta}, RelationCounterDelta{target, "follower_count", delta})
	}
	return deltas
}
