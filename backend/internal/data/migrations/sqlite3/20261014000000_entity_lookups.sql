-- +goose Up
-- Product lookups: what was found out about an item on the web, waiting for review.
--   entity_lookups  one per item; status, the query, and the candidate matches as JSON
create table if not exists entity_lookups
(
    id         uuid         not null
        primary key,
    created_at datetime     not null,
    updated_at datetime     not null,
    status     text         not null default 'pending',
    query      varchar(500) not null default '',
    candidates json,
    error      varchar(500) not null default '',
    attempts   integer      not null default 0,
    entity_id  uuid         not null
        constraint entity_lookups_entities_lookups
            references entities
            on delete cascade,
    group_id   uuid         not null
        constraint entity_lookups_groups_entity_lookups
            references groups
            on delete cascade
);
create unique index if not exists entitylookup_entity_id on entity_lookups (entity_id);
create index if not exists entitylookup_status_created_at on entity_lookups (status, created_at);
create index if not exists entitylookup_group_id_status on entity_lookups (group_id, status);

-- +goose Down
drop table if exists entity_lookups;
