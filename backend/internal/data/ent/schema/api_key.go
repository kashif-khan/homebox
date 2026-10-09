package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// APIKey holds the schema definition for static, user-issued API keys that
// authenticate as the owning user.
type APIKey struct {
	ent.Schema
}

func (APIKey) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		UserMixin{
			ref:   "api_keys",
			field: "user_id",
		},
	}
}

func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(255).
			NotEmpty(),
		field.Bytes("token").
			Unique().
			Sensitive(),
		field.Time("expires_at").
			Optional().
			Nillable(),
		field.Time("last_used_at").
			Optional().
			Nillable(),
		// scopes limits what the key may do. Rows created before scopes existed
		// are backfilled with ["*"] (full access) by the migration.
		field.Strings("scopes").
			Default([]string{"*"}),
		// group_id pins the key to a single collection. NULL means the key may
		// act in any collection its owner belongs to.
		field.UUID("group_id", uuid.UUID{}).
			Optional().
			Nillable(),
	}
}

func (APIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group", Group.Type).
			Field("group_id").
			Unique().
			Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
	}
}

func (APIKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("token"),
		index.Fields("user_id"),
	}
}
