package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// EntityType holds the schema definition for the EntityType entity.
type EntityType struct {
	ent.Schema
}

func (EntityType) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		mixins.DetailsMixin{},
		GroupMixin{ref: "entity_types"},
	}
}

// Fields of the EntityType.
func (EntityType) Fields() []ent.Field {
	return []ent.Field{
		field.Bool("is_location").
			Default(false),
		field.String("icon").
			MaxLen(255).
			Optional(),
		// expiry_required makes an expiry date mandatory on entities of this
		// type, for example medicine or pantry food.
		field.Bool("expiry_required").
			Default(false),
	}
}

// Edges of the EntityType.
func (EntityType) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("entities", Entity.Type).
			Annotations(entsql.Annotation{
				OnDelete: entsql.Restrict,
			}),
		edge.To("default_template", EntityTemplate.Type).
			Unique(),
	}
}
