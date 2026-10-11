-- +goose Up
-- Outbound webhooks: signed HTTP callbacks for events.
--   webhook_endpoints   where to send, which events, and the signing secret
--   webhook_deliveries  one event owed to one endpoint, retried until sent or too old
create table if not exists webhook_endpoints
(
    id             uuid          not null
        primary key,
    created_at     datetime      not null,
    updated_at     datetime      not null,
    name           varchar(255)  not null,
    url            varchar(2083) not null,
    secret         varchar(128)  not null,
    events         json          not null,
    entity_type_id uuid,
    tag_id         uuid,
    is_active      bool          not null default true,
    group_id       uuid          not null
        constraint webhook_endpoints_groups_webhook_endpoints
            references groups
            on delete cascade
);
create index if not exists webhookendpoint_group_id on webhook_endpoints (group_id);
create index if not exists webhookendpoint_group_id_is_active on webhook_endpoints (group_id, is_active);

create table if not exists webhook_deliveries
(
    id                          uuid         not null
        primary key,
    created_at                  datetime     not null,
    updated_at                  datetime     not null,
    event_type                  varchar(64)  not null,
    event_id                    uuid         not null,
    occurred_at                 datetime     not null,
    scheduled_for               datetime,
    data                        json         not null,
    dedupe_key                  varchar(255),
    status                      text         not null default 'pending',
    attempts                    integer      not null default 0,
    next_attempt_at             datetime     not null,
    delivered_at                datetime,
    last_status_code            integer      not null default 0,
    last_error                  varchar(500) not null default '',
    last_duration_ms            integer      not null default 0,
    response_snippet            varchar(500) not null default '',
    webhook_endpoint_deliveries uuid         not null
        constraint webhook_deliveries_webhook_endpoints_deliveries
            references webhook_endpoints
            on delete cascade
);
create index if not exists webhookdelivery_status_next_attempt_at on webhook_deliveries (status, next_attempt_at);
create index if not exists webhookdelivery_created_at on webhook_deliveries (created_at);
create unique index if not exists webhookdelivery_dedupe on webhook_deliveries (dedupe_key, webhook_endpoint_deliveries);

-- +goose Down
drop table if exists webhook_deliveries;
drop table if exists webhook_endpoints;
