package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// OAuthRequest is an authorization request waiting for the user's decision. It
// lives for minutes. Its random id is handed to the consent page, so it also
// serves as the CSRF token for the approve and deny actions.
type OAuthRequest struct {
	ent.Schema
}

func (OAuthRequest) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (OAuthRequest) Fields() []ent.Field {
	return []ent.Field{
		field.String("redirect_uri").NotEmpty().MaxLen(2048),
		field.Strings("scopes"),
		field.String("state").Optional().MaxLen(2048),
		field.String("code_challenge").NotEmpty().MaxLen(128),
		field.String("resource").Optional().MaxLen(2048),
		field.Time("expires_at"),
	}
}

func (OAuthRequest) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("client", OAuthClient.Type).
			Ref("requests").
			Unique().
			Required(),
	}
}

func (OAuthRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("expires_at")}
}
