package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// OAuthClient is an application (an AI assistant, typically) registered with the
// built-in authorization server, usually through dynamic client registration.
// Clients are public: they authenticate with PKCE, not a secret.
type OAuthClient struct {
	ent.Schema
}

func (OAuthClient) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (OAuthClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_id").
			Unique().
			NotEmpty().
			MaxLen(64),
		field.String("name").
			NotEmpty().
			MaxLen(255),
		field.Strings("redirect_uris"),
	}
}

func (OAuthClient) Edges() []ent.Edge {
	cascade := entsql.Annotation{OnDelete: entsql.Cascade}
	return []ent.Edge{
		edge.To("grants", OAuthGrant.Type).Annotations(cascade),
		edge.To("requests", OAuthRequest.Type).Annotations(cascade),
	}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{index.Fields("client_id")}
}
