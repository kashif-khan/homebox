package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// OAuthToken is one issued secret: an authorization code, an access token or a
// refresh token. Only the keyed hash is stored.
type OAuthToken struct {
	ent.Schema
}

func (OAuthToken) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (OAuthToken) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").Values("code", "access", "refresh"),
		field.Bytes("token_hash").Unique().Sensitive(),
		field.Time("expires_at"),
		// used_at marks a code or refresh token as spent. Presenting a spent one
		// again is treated as theft and revokes the whole grant.
		field.Time("used_at").Optional().Nillable(),
		// Codes carry what the token request must prove it still matches.
		field.String("code_challenge").Optional().MaxLen(128),
		field.String("redirect_uri").Optional().MaxLen(2048),
	}
}

func (OAuthToken) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("grant", OAuthGrant.Type).
			Ref("tokens").
			Unique().
			Required(),
	}
}

func (OAuthToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("token_hash"),
		index.Fields("expires_at"),
	}
}
