-- +goose Up
-- Scoped API keys. `scopes` limits what a key may do; `group_id` optionally pins
-- it to a single collection.
--
-- Keys that already exist authenticate with full access today, so they are
-- backfilled with the full-access wildcard to keep working unchanged. Keys
-- created from now on default to read-only at the API layer.
alter table api_keys
    add column scopes json not null default '["*"]';

alter table api_keys
    add column group_id uuid
        constraint api_keys_groups_group
            references groups
            on delete cascade;

create index if not exists apikey_group_id
    on api_keys (group_id);

-- +goose Down
drop index if exists apikey_group_id;
alter table api_keys drop column group_id;
alter table api_keys drop column scopes;
