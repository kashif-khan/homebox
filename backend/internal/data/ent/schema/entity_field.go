package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// EntityField holds the schema definition for the EntityField entity.
type EntityField struct {
	ent.Schema
}

func (EntityField) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		mixins.DetailsMixin{},
	}
}

// Fields of the EntityField.
func (EntityField) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("type").
			Values("text", "number", "boolean", "time",
				"decimal", "currency", "date", "datetime", "url", "select"),
		field.String("text_value").
			MaxLen(500).
			Optional(),
		field.Int("number_value").
			Optional(),
		field.Bool("boolean_value").
			Default(false),
		field.Time("time_value").
			Default(time.Now),
		// Typed columns derived from the canonical text_value so the database can
		// sort and filter on real types. decimal_value backs "decimal"; a
		// "currency" amount is stored as minor units in number_value with its
		// ISO 4217 code in currency_code, so there is no float rounding. For
		// "date" and "datetime", time_value holds the UTC instant, and timezone
		// the IANA zone a datetime was entered in. choices backs "select".
		field.Float("decimal_value").
			Optional().
			Nillable(),
		field.String("currency_code").
			MaxLen(3).
			Default(""),
		field.String("timezone").
			MaxLen(64).
			Default(""),
		field.JSON("choices", []string{}).
			Optional(),
	}
}

// Edges of the EntityField.
func (EntityField) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("entity", Entity.Type).
			Ref("fields").
			Unique(),
	}
}
