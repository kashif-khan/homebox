-- +goose Up
-- Per-collection ceiling for AI assistants connected over MCP, set by the
-- collection owner: off, read, write or full. Defaults to off, so MCP is opt-in
-- for every existing collection.
ALTER TABLE "groups" ADD COLUMN IF NOT EXISTS "mcp_access" character varying NOT NULL DEFAULT 'off';

-- +goose Down
ALTER TABLE "groups" DROP COLUMN IF EXISTS "mcp_access";
