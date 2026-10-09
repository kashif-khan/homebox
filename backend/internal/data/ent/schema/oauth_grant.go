package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// OAuthGrant is a user's standing consent for one client to act as them in one
// collection with a set of scopes. Deleting it revokes every token issued under
// it, which is how "disconnect this app" works.
type OAuthGrant struct {
	ent.Schema
}

func (OAuthGrant) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		UserMixin{ref: "oauth_grants", field: "user_id"},
		GroupMixin{ref: "oauth_grants", field: "group_id"},
	}
}

func (OAuthGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Strings("scopes"),
		field.Time("last_used_at").Optional().Nillable(),
	}
}

func (OAuthGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("client", OAuthClient.Type).
			Ref("grants").
			Unique().
			Required(),
		edge.To("tokens", OAuthToken.Type).
			Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
	}
}
