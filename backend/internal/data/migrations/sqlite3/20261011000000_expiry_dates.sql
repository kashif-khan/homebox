-- +goose Up
-- Expiry dates for consumables, whether a type requires one, and the template
-- defaults that fill it in. All additive.
alter table entities add column expires_at datetime;
alter table entities add column expiry_kind text not null default 'expires';
create index if not exists entity_expires_at on entities(expires_at);
alter table entity_types add column expiry_required bool not null default false;
alter table entity_templates add column include_expiry_fields bool not null default false;
alter table entity_templates add column default_expiry_offset_days integer;
alter table entity_templates add column default_expiry_kind text not null default 'expires';

-- +goose Down
alter table entity_templates drop column default_expiry_kind;
alter table entity_templates drop column default_expiry_offset_days;
alter table entity_templates drop column include_expiry_fields;
alter table entity_types drop column expiry_required;
drop index if exists entity_expires_at;
alter table entities drop column expiry_kind;
alter table entities drop column expires_at;
