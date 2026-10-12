-- +goose Up
-- Product lookups: what was found out about an item on the web, waiting for review.
--   entity_lookups  one per item; status, the query, and the candidate matches as JSON
CREATE TABLE IF NOT EXISTS "entity_lookups" (
    "id"         uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "status"     character varying NOT NULL DEFAULT 'pending',
    "query"      character varying(500) NOT NULL DEFAULT '',
    "candidates" jsonb NULL,
    "error"      character varying(500) NOT NULL DEFAULT '',
    "attempts"   bigint NOT NULL DEFAULT 0,
    "entity_id"  uuid NOT NULL,
    "group_id"   uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "entity_lookups_entities_lookups" FOREIGN KEY ("entity_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "entity_lookups_groups_entity_lookups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "entitylookup_entity_id" ON "entity_lookups" ("entity_id");
CREATE INDEX IF NOT EXISTS "entitylookup_status_created_at" ON "entity_lookups" ("status", "created_at");
CREATE INDEX IF NOT EXISTS "entitylookup_group_id_status" ON "entity_lookups" ("group_id", "status");

-- +goose Down
DROP TABLE IF EXISTS "entity_lookups";
