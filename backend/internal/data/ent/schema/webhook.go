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

// WebhookEndpoint is a URL a collection wants told about events. The URL often
// carries a token (n8n, Home Assistant) and the secret signs every delivery, so
// both are Sensitive: kept out of logs and never returned by the API.
type WebhookEndpoint struct {
	ent.Schema
}

func (WebhookEndpoint) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		GroupMixin{ref: "webhook_endpoints", field: "group_id"},
	}
}

func (WebhookEndpoint) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(255).
			NotEmpty(),
		field.String("url").
			Sensitive().
			MaxLen(2083).
			NotEmpty(),
		field.String("secret").
			Sensitive().
			MaxLen(128).
			NotEmpty(),
		field.JSON("events", []string{}),
		// Optional filters for item events. Plain ids: a deleted type or tag
		// simply stops matching.
		field.UUID("entity_type_id", uuid.UUID{}).
			Optional().
			Nillable(),
		field.UUID("tag_id", uuid.UUID{}).
			Optional().
			Nillable(),
		field.Bool("is_active").
			Default(true),
	}
}

func (WebhookEndpoint) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id"),
		index.Fields("group_id", "is_active"),
	}
}

func (WebhookEndpoint) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("deliveries", WebhookDelivery.Type).
			Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
	}
}

// WebhookDelivery is one event owed to one endpoint, and the log of trying to
// deliver it. Rows are written when the event happens, so a delivery survives a
// restart and is retried until it succeeds or is too old.
type WebhookDelivery struct {
	ent.Schema
}

func (WebhookDelivery) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
	}
}

func (WebhookDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.String("event_type").
			MaxLen(64).
			NotEmpty(),
		// event_id is shared by every endpoint's delivery of one event.
		field.UUID("event_id", uuid.UUID{}),
		// occurred_at is when the thing happened; for a date-driven event it is
		// the instant the alert was due. scheduled_for is set only for those.
		field.Time("occurred_at"),
		field.Time("scheduled_for").
			Optional().
			Nillable(),
		field.JSON("data", map[string]any{}),
		// dedupe_key makes an event that can be offered more than once (a date
		// alert seen on several ticks) create a single delivery per endpoint.
		field.String("dedupe_key").
			MaxLen(255).
			Optional().
			Nillable(),
		field.Enum("status").
			Values("pending", "delivered", "failed").
			Default("pending"),
		field.Int("attempts").
			Default(0),
		field.Time("next_attempt_at"),
		field.Time("delivered_at").
			Optional().
			Nillable(),
		field.Int("last_status_code").
			Default(0),
		field.String("last_error").
			MaxLen(500).
			Default(""),
		field.Int("last_duration_ms").
			Default(0),
		field.String("response_snippet").
			MaxLen(500).
			Default(""),
	}
}

func (WebhookDelivery) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "next_attempt_at"),
		index.Fields("created_at"),
		index.Fields("dedupe_key").
			Edges("endpoint").
			StorageKey("webhookdelivery_dedupe").
			Unique(),
	}
}

func (WebhookDelivery) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("endpoint", WebhookEndpoint.Type).
			Ref("deliveries").
			Unique().
			Required(),
	}
}
