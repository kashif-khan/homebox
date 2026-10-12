package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// EntityLookup is the product lookup for one item: what was asked, whether an
// answer has arrived, and the candidates waiting for the user to review. There is
// at most one per item. Rows are written when a lookup is requested, so a lookup
// survives a restart and is picked up by the worker when it comes back.
type EntityLookup struct {
	ent.Schema
}

func (EntityLookup) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		GroupMixin{ref: "entity_lookups", field: "group_id"},
	}
}

func (EntityLookup) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("entity_id", uuid.UUID{}),
		field.Enum("status").
			Values("pending", "done", "failed").
			Default("pending"),
		field.String("query").
			MaxLen(500).
			Default(""),
		field.JSON("candidates", []types.LookupCandidate{}).
			Optional(),
		field.String("error").
			MaxLen(500).
			Default(""),
		field.Int("attempts").
			Default(0),
	}
}

func (EntityLookup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("entity_id").Unique(),
		index.Fields("status", "created_at"),
		index.Fields("group_id", "status"),
	}
}

func (EntityLookup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("entity", Entity.Type).
			Ref("lookups").
			Field("entity_id").
			Unique().
			Required(),
	}
}
