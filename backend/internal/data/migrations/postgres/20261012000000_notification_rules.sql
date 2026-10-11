-- +goose Up
-- Alert rules: tell notifiers N days before or after an expiry, a warranty end
-- or a maintenance due date. notification_logs remembers what was sent so each
-- alert fires once. groups gain the local hour alerts go out and a flag that the
-- default maintenance rule has been created.
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "notify_hour" bigint NOT NULL DEFAULT 8;
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "alert_rules_seeded" boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS "notification_rules" (
    "id"             uuid NOT NULL,
    "created_at"     timestamptz NOT NULL,
    "updated_at"     timestamptz NOT NULL,
    "name"           character varying(255) NOT NULL,
    "trigger"        character varying NOT NULL,
    "offsets_days"   jsonb NOT NULL,
    "entity_type_id" uuid NULL,
    "tag_id"         uuid NULL,
    "notifier_ids"   jsonb NULL,
    "is_active"      boolean NOT NULL DEFAULT true,
    "group_id"       uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "notification_rules_groups_notification_rules" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "notificationrule_group_id" ON "notification_rules" ("group_id");
CREATE INDEX IF NOT EXISTS "notificationrule_group_id_is_active" ON "notification_rules" ("group_id", "is_active");

CREATE TABLE IF NOT EXISTS "notification_logs" (
    "id"                     uuid NOT NULL,
    "created_at"             timestamptz NOT NULL,
    "updated_at"             timestamptz NOT NULL,
    "notifier_id"            uuid NOT NULL,
    "subject_id"             uuid NOT NULL,
    "offset_days"            bigint NOT NULL,
    "anchor_date"            timestamptz NOT NULL,
    "due_on"                 timestamptz NOT NULL,
    "sent_at"                timestamptz NOT NULL,
    "notification_rule_logs" uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "notification_logs_notification_rules_logs" FOREIGN KEY ("notification_rule_logs") REFERENCES "notification_rules" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "notificationlog_once" ON "notification_logs" ("notifier_id", "subject_id", "offset_days", "anchor_date", "notification_rule_logs");
CREATE INDEX IF NOT EXISTS "notificationlog_sent_at" ON "notification_logs" ("sent_at");

-- +goose Down
DROP TABLE IF EXISTS "notification_logs";
DROP TABLE IF EXISTS "notification_rules";
ALTER TABLE "groups" DROP COLUMN IF EXISTS "alert_rules_seeded";
ALTER TABLE "groups" DROP COLUMN IF EXISTS "notify_hour";
