package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShepherdAck records that the user dismissed one needs-attention item (#599).
// The key is the item's stable fingerprint (`kind:target…`), so re-collecting
// the same problem never shows it again until its fingerprint changes (a new
// failure task id, a new review round, a new circuit trip count).
type ShepherdAck struct {
	ent.Schema
}

// Fields of the ShepherdAck.
func (ShepherdAck) Fields() []ent.Field {
	return []ent.Field{
		field.String("fingerprint").
			Unique().
			NotEmpty().
			Comment("확인한 attention 항목의 지문 (kind:대상ID)"),
		field.Time("acked_at").
			Default(time.Now).
			Comment("확인한 시각 (30일 후 정리, 재확인하면 갱신)"),
	}
}

// Indexes lets the 30-day prune query scan by time without a full table walk.
func (ShepherdAck) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("acked_at"),
	}
}

// Edges of the ShepherdAck.
func (ShepherdAck) Edges() []ent.Edge {
	return nil
}
