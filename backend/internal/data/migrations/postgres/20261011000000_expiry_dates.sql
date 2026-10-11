-- +goose Up
-- Expiry dates for consumables, whether a type requires one, and the template
-- defaults that fill it in. All additive.
ALTER TABLE "entities" ADD COLUMN IF NOT EXISTS "expires_at" timestamptz NULL;
ALTER TABLE "entities" ADD COLUMN IF NOT EXISTS "expiry_kind" character varying NOT NULL DEFAULT 'expires';
CREATE INDEX IF NOT EXISTS "entity_expires_at" ON "entities" ("expires_at");
ALTER TABLE "entity_types" ADD COLUMN IF NOT EXISTS "expiry_required" boolean NOT NULL DEFAULT false;
ALTER TABLE "entity_templates" ADD COLUMN IF NOT EXISTS "include_expiry_fields" boolean NOT NULL DEFAULT false;
ALTER TABLE "entity_templates" ADD COLUMN IF NOT EXISTS "default_expiry_offset_days" bigint NULL;
ALTER TABLE "entity_templates" ADD COLUMN IF NOT EXISTS "default_expiry_kind" character varying NOT NULL DEFAULT 'expires';

-- +goose Down
ALTER TABLE "entity_templates" DROP COLUMN IF EXISTS "default_expiry_kind";
ALTER TABLE "entity_templates" DROP COLUMN IF EXISTS "default_expiry_offset_days";
ALTER TABLE "entity_templates" DROP COLUMN IF EXISTS "include_expiry_fields";
ALTER TABLE "entity_types" DROP COLUMN IF EXISTS "expiry_required";
DROP INDEX IF EXISTS "entity_expires_at";
ALTER TABLE "entities" DROP COLUMN IF EXISTS "expiry_kind";
ALTER TABLE "entities" DROP COLUMN IF EXISTS "expires_at";
