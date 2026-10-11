-- +goose Up
-- Typed custom fields. The new types (decimal, currency, date, datetime, url,
-- select) keep a canonical string in text_value and derive these columns from
-- it. All additive, so existing fields are unchanged.
alter table entity_fields add column decimal_value real;
alter table entity_fields add column currency_code text not null default '';
alter table entity_fields add column timezone text not null default '';
alter table entity_fields add column choices json;
alter table template_fields add column decimal_value real;
alter table template_fields add column currency_code text not null default '';
alter table template_fields add column timezone text not null default '';
alter table template_fields add column choices json;

-- +goose Down
alter table template_fields drop column choices;
alter table template_fields drop column timezone;
alter table template_fields drop column currency_code;
alter table template_fields drop column decimal_value;
alter table entity_fields drop column choices;
alter table entity_fields drop column timezone;
alter table entity_fields drop column currency_code;
alter table entity_fields drop column decimal_value;
