-- +goose Up
-- Outbound webhooks: signed HTTP callbacks for events.
--   webhook_endpoints   where to send, which events, and the signing secret
--   webhook_deliveries  one event owed to one endpoint, retried until sent or too old
CREATE TABLE IF NOT EXISTS "webhook_endpoints" (
    "id"             uuid NOT NULL,
    "created_at"     timestamptz NOT NULL,
    "updated_at"     timestamptz NOT NULL,
    "name"           character varying(255) NOT NULL,
    "url"            character varying(2083) NOT NULL,
    "secret"         character varying(128) NOT NULL,
    "events"         jsonb NOT NULL,
    "entity_type_id" uuid NULL,
    "tag_id"         uuid NULL,
    "is_active"      boolean NOT NULL DEFAULT true,
    "group_id"       uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "webhook_endpoints_groups_webhook_endpoints" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "webhookendpoint_group_id" ON "webhook_endpoints" ("group_id");
CREATE INDEX IF NOT EXISTS "webhookendpoint_group_id_is_active" ON "webhook_endpoints" ("group_id", "is_active");

CREATE TABLE IF NOT EXISTS "webhook_deliveries" (
    "id"                          uuid NOT NULL,
    "created_at"                  timestamptz NOT NULL,
    "updated_at"                  timestamptz NOT NULL,
    "event_type"                  character varying(64) NOT NULL,
    "event_id"                    uuid NOT NULL,
    "occurred_at"                 timestamptz NOT NULL,
    "scheduled_for"               timestamptz NULL,
    "data"                        jsonb NOT NULL,
    "dedupe_key"                  character varying(255) NULL,
    "status"                      character varying NOT NULL DEFAULT 'pending',
    "attempts"                    bigint NOT NULL DEFAULT 0,
    "next_attempt_at"             timestamptz NOT NULL,
    "delivered_at"                timestamptz NULL,
    "last_status_code"            bigint NOT NULL DEFAULT 0,
    "last_error"                  character varying(500) NOT NULL DEFAULT '',
    "last_duration_ms"            bigint NOT NULL DEFAULT 0,
    "response_snippet"            character varying(500) NOT NULL DEFAULT '',
    "webhook_endpoint_deliveries" uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "webhook_deliveries_webhook_endpoints_deliveries" FOREIGN KEY ("webhook_endpoint_deliveries") REFERENCES "webhook_endpoints" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "webhookdelivery_status_next_attempt_at" ON "webhook_deliveries" ("status", "next_attempt_at");
CREATE INDEX IF NOT EXISTS "webhookdelivery_created_at" ON "webhook_deliveries" ("created_at");
CREATE UNIQUE INDEX IF NOT EXISTS "webhookdelivery_dedupe" ON "webhook_deliveries" ("dedupe_key", "webhook_endpoint_deliveries");

-- +goose Down
DROP TABLE IF EXISTS "webhook_deliveries";
DROP TABLE IF EXISTS "webhook_endpoints";
