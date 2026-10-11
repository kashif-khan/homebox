-- +goose Up
-- Regional preferences for users, and the timezone a collection schedules its
-- alerts in. All additive, with defaults, so existing rows keep working.
alter table users add column country text not null default '';
alter table users add column timezone text not null default '';
alter table users add column locale text not null default '';
alter table groups add column timezone text not null default 'UTC';

-- +goose Down
alter table groups drop column timezone;
alter table users drop column locale;
alter table users drop column timezone;
alter table users drop column country;
