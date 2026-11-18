package events

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const RelationChangedType = "relation.changed.v1"

type RelationChangedPayload struct {
	ActorID       string `json:"actor_id"`
	TargetID      string `json:"target_id"`
	Version       int64  `json:"version"`
	Before        string `json:"before"`
	After         string `json:"after"`
	ReverseBefore string `json:"reverse_before,omitempty"`
	ReverseAfter  string `json:"reverse_after,omitempty"`
}

type RelationChangedEvent struct {
	SchemaVersion int                    `json:"schema_version"`
	EventID       string                 `json:"event_id"`
	Type          string                 `json:"type"`
	OccurredAt    time.Time              `json:"occurred_at"`
	Payload       RelationChangedPayload `json:"payload"`
}

func (e RelationChangedEvent) Validate() error {
	if e.SchemaVersion != 1 || e.Type != RelationChangedType {
		return fmt.Errorf("unsupported relation envelope")
	}
	actor, err := relationDecimalID(e.Payload.ActorID)
	if err != nil {
		return err
	}
	target, err := relationDecimalID(e.Payload.TargetID)
	if err != nil {
		return err
	}
	if actor == target || e.Payload.Version <= 0 {
		return fmt.Errorf("invalid relation pair or version")
	}
	low, high := actor, target
	if low > high {
		low, high = high, low
	}
	if e.EventID != fmt.Sprintf("relation:%d:%d:%d", low, high, e.Payload.Version) {
		return fmt.Errorf("relation event identity mismatch")
	}
	_, offset := e.OccurredAt.Zone()
	if e.OccurredAt.IsZero() || offset != 0 {
		return fmt.Errorf("relation occurrence must be UTC")
	}
	p := e.Payload
	validState := func(s string) bool { return s == "none" || s == "follow" || s == "black" }
	if !validState(p.Before) || !validState(p.After) {
		return fmt.Errorf("invalid relation state")
	}
	reverseBefore, reverseAfter := p.ReverseBefore, p.ReverseAfter
	if (reverseBefore == "") != (reverseAfter == "") {
		return fmt.Errorf("incomplete reverse relation state")
	}
	if reverseBefore == "" {
		reverseBefore, reverseAfter = "none", "none"
	}
	if !validState(reverseBefore) || !validState(reverseAfter) {
		return fmt.Errorf("invalid reverse relation state")
	}
	if p.Before == p.After && reverseBefore == reverseAfter {
		return fmt.Errorf("unchanged relation event")
	}
	switch p.After {
	case "follow":
		if p.Before != "none" || reverseBefore == "black" || reverseBefore != reverseAfter {
			return fmt.Errorf("invalid follow transition")
		}
	case "none":
		if (p.Before != "follow" && p.Before != "black") || reverseBefore != reverseAfter {
			return fmt.Errorf("invalid relation removal")
		}
	case "black":
		if reverseAfter == "follow" || (reverseBefore == "follow" && reverseAfter != "none") || (reverseBefore == "black" && reverseAfter != "black") || (reverseBefore == "none" && reverseAfter != "none") {
			return fmt.Errorf("invalid block transition")
		}
	}
	return nil
}

func relationDecimalID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, fmt.Errorf("invalid decimal relation ID")
	}
	return id, nil
}

func (e RelationChangedEvent) ToNotification() (*Event, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.Payload.After != "follow" {
		return nil, nil
	}
	return &Event{EventID: e.EventID, Type: NotifFollow, RecipientID: e.Payload.TargetID, ActorID: e.Payload.ActorID, TargetID: e.Payload.TargetID, TargetType: TargetUser, CreatedAt: e.OccurredAt.Unix()}, nil
}

func DecodeRelationNotification(raw []byte) (*Event, error) {
	var e RelationChangedEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("decode relation event: %w", err)
	}
	return e.ToNotification()
}
