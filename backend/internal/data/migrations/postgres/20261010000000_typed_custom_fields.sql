-- +goose Up
-- Typed custom fields. The new types (decimal, currency, date, datetime, url,
-- select) keep a canonical string in text_value and derive these columns from
-- it. All additive, so existing fields are unchanged.
ALTER TABLE "entity_fields" ADD COLUMN IF NOT EXISTS "decimal_value" double precision NULL;
ALTER TABLE "entity_fields" ADD COLUMN IF NOT EXISTS "currency_code" character varying NOT NULL DEFAULT '';
ALTER TABLE "entity_fields" ADD COLUMN IF NOT EXISTS "timezone" character varying NOT NULL DEFAULT '';
ALTER TABLE "entity_fields" ADD COLUMN IF NOT EXISTS "choices" jsonb NULL;
ALTER TABLE "template_fields" ADD COLUMN IF NOT EXISTS "decimal_value" double precision NULL;
ALTER TABLE "template_fields" ADD COLUMN IF NOT EXISTS "currency_code" character varying NOT NULL DEFAULT '';
ALTER TABLE "template_fields" ADD COLUMN IF NOT EXISTS "timezone" character varying NOT NULL DEFAULT '';
ALTER TABLE "template_fields" ADD COLUMN IF NOT EXISTS "choices" jsonb NULL;

-- +goose Down
ALTER TABLE "template_fields" DROP COLUMN IF EXISTS "choices";
ALTER TABLE "template_fields" DROP COLUMN IF EXISTS "timezone";
ALTER TABLE "template_fields" DROP COLUMN IF EXISTS "currency_code";
ALTER TABLE "template_fields" DROP COLUMN IF EXISTS "decimal_value";
ALTER TABLE "entity_fields" DROP COLUMN IF EXISTS "choices";
ALTER TABLE "entity_fields" DROP COLUMN IF EXISTS "timezone";
ALTER TABLE "entity_fields" DROP COLUMN IF EXISTS "currency_code";
ALTER TABLE "entity_fields" DROP COLUMN IF EXISTS "decimal_value";
