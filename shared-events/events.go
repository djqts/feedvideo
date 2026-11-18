package events

// NotifType 通知类型
type NotifType string

const (
	NotifLike    NotifType = "like"
	NotifComment NotifType = "comment"
	NotifFollow  NotifType = "follow"
	NotifSystem  NotifType = "system"
)

// TargetType 目标对象类型
const (
	TargetVideo   = "video"
	TargetComment = "comment"
	TargetUser    = "user"
)

// Event 互动通知事件，经 RabbitMQ 以 JSON 传输。
type Event struct {
	// EventID 幂等键（P3.2）：Outbox 写入与消费去重依赖该字段。
	EventID     string    `json:"event_id,omitempty"`
	Type        NotifType `json:"type"`
	RecipientID string    `json:"recipient_id"`
	ActorID     string    `json:"actor_id"`
	TargetID    string    `json:"target_id"`
	TargetType  string    `json:"target_type"`
	Content     string    `json:"content"`
	CreatedAt   int64     `json:"created_at"` // Unix 秒
}
