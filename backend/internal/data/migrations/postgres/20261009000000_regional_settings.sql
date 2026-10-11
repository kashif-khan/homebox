-- +goose Up
-- Regional preferences for users, and the timezone a collection schedules its
-- alerts in. All additive, with defaults, so existing rows keep working.
ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "country" character varying NOT NULL DEFAULT '';
ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "timezone" character varying NOT NULL DEFAULT '';
ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "locale" character varying NOT NULL DEFAULT '';
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "timezone" character varying NOT NULL DEFAULT 'UTC';

-- +goose Down
ALTER TABLE "groups" DROP COLUMN IF EXISTS "timezone";
ALTER TABLE "users" DROP COLUMN IF EXISTS "locale";
ALTER TABLE "users" DROP COLUMN IF EXISTS "timezone";
ALTER TABLE "users" DROP COLUMN IF EXISTS "country";
