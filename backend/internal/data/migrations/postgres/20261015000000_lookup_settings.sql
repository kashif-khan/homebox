-- +goose Up
-- Product lookup settings, kept per collection so they can be set from the web
-- UI instead of through the server's environment: the lookup service to ask, the
-- token for it, and whether every new item is looked up automatically.
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "lookup_url" character varying(2083) NOT NULL DEFAULT '';
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "lookup_token" character varying(512) NOT NULL DEFAULT '';
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "lookup_auto" boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE "groups" DROP COLUMN IF EXISTS "lookup_auto";
ALTER TABLE "groups" DROP COLUMN IF EXISTS "lookup_token";
ALTER TABLE "groups" DROP COLUMN IF EXISTS "lookup_url";
