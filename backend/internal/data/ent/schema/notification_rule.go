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

// NotificationRule says when to tell people about a date: how many days before
// or after an expiry, a warranty end or a maintenance due date, which items it
// covers and which notifiers receive it.
type NotificationRule struct {
	ent.Schema
}

func (NotificationRule) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
		GroupMixin{ref: "notification_rules", field: "group_id"},
	}
}

func (NotificationRule) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(255).
			NotEmpty(),
		field.Enum("trigger").
			Values("expires_at", "warranty_expires", "maintenance_due"),
		// offsets_days: positive fires that many days before the date, zero on
		// the day, negative that many days after.
		field.JSON("offsets_days", []int{}),
		// Optional filters. Plain ids rather than edges: a deleted type or tag
		// simply stops matching.
		field.UUID("entity_type_id", uuid.UUID{}).
			Optional().
			Nillable(),
		field.UUID("tag_id", uuid.UUID{}).
			Optional().
			Nillable(),
		// notifier_ids empty means every active notifier of the collection.
		field.JSON("notifier_ids", []uuid.UUID{}).
			Optional(),
		field.Bool("is_active").
			Default(true),
	}
}

func (NotificationRule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id"),
		index.Fields("group_id", "is_active"),
	}
}

func (NotificationRule) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("logs", NotificationLog.Type).
			Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
	}
}

// NotificationLog records that one notifier was told about one subject at one
// offset of one date, so an alert fires once rather than on every run. The
// anchor date is part of the key: move the date and the alert is due again.
type NotificationLog struct {
	ent.Schema
}

func (NotificationLog) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.BaseMixin{},
	}
}

func (NotificationLog) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("notifier_id", uuid.UUID{}),
		field.UUID("subject_id", uuid.UUID{}),
		field.Int("offset_days"),
		field.Time("anchor_date"),
		field.Time("due_on"),
		field.Time("sent_at"),
	}
}

func (NotificationLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("notifier_id", "subject_id", "offset_days", "anchor_date").
			Edges("rule").
			StorageKey("notificationlog_once").
			Unique(),
		index.Fields("sent_at"),
	}
}

func (NotificationLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("rule", NotificationRule.Type).
			Ref("logs").
			Unique().
			Required(),
	}
}
