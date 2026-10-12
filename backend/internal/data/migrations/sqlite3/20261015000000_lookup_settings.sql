-- +goose Up
-- Product lookup settings, kept per collection so they can be set from the web
-- UI instead of through the server's environment: the lookup service to ask, the
-- token for it, and whether every new item is looked up automatically.
alter table groups
    add column lookup_url varchar(2083) not null default '';
alter table groups
    add column lookup_token varchar(512) not null default '';
alter table groups
    add column lookup_auto bool not null default false;

-- +goose Down
alter table groups drop column lookup_auto;
alter table groups drop column lookup_token;
alter table groups drop column lookup_url;
