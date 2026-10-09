-- +goose Up
-- Per-collection ceiling for AI assistants connected over MCP, set by the
-- collection owner: off, read, write or full. Defaults to off, so MCP is opt-in
-- for every existing collection.
alter table groups
    add column mcp_access text not null default 'off';

-- +goose Down
alter table groups drop column mcp_access;
