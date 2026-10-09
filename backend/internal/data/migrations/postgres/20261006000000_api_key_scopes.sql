-- +goose Up
-- Scoped API keys. `scopes` limits what a key may do; `group_id` optionally pins
-- it to a single collection.
--
-- Keys that already exist authenticate with full access today, so they are
-- backfilled with the full-access wildcard to keep working unchanged. Keys
-- created from now on default to read-only at the API layer.
ALTER TABLE "api_keys" ADD COLUMN IF NOT EXISTS "scopes" jsonb NOT NULL DEFAULT '["*"]'::jsonb;
ALTER TABLE "api_keys" ADD COLUMN IF NOT EXISTS "group_id" uuid NULL;
ALTER TABLE "api_keys" ADD CONSTRAINT "api_keys_groups_group" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS "apikey_group_id" ON "api_keys" ("group_id");

-- +goose Down
DROP INDEX IF EXISTS "apikey_group_id";
ALTER TABLE "api_keys" DROP CONSTRAINT IF EXISTS "api_keys_groups_group";
ALTER TABLE "api_keys" DROP COLUMN IF EXISTS "group_id";
ALTER TABLE "api_keys" DROP COLUMN IF EXISTS "scopes";
