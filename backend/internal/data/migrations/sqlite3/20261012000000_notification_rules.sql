-- +goose Up
-- Alert rules: tell notifiers N days before or after an expiry, a warranty end
-- or a maintenance due date. notification_logs remembers what was sent so each
-- alert fires once. groups gain the local hour alerts go out and a flag that the
-- default maintenance rule has been created.
alter table groups add column notify_hour integer not null default 8;
alter table groups add column alert_rules_seeded bool not null default false;

create table if not exists notification_rules
(
    id             uuid         not null
        primary key,
    created_at     datetime     not null,
    updated_at     datetime     not null,
    name           varchar(255) not null,
    trigger        text         not null,
    offsets_days   json         not null,
    entity_type_id uuid,
    tag_id         uuid,
    notifier_ids   json,
    is_active      bool         not null default true,
    group_id       uuid         not null
        constraint notification_rules_groups_notification_rules
            references groups
            on delete cascade
);
create index if not exists notificationrule_group_id on notification_rules (group_id);
create index if not exists notificationrule_group_id_is_active on notification_rules (group_id, is_active);

create table if not exists notification_logs
(
    id                     uuid     not null
        primary key,
    created_at             datetime not null,
    updated_at             datetime not null,
    notifier_id            uuid     not null,
    subject_id             uuid     not null,
    offset_days            integer  not null,
    anchor_date            datetime not null,
    due_on                 datetime not null,
    sent_at                datetime not null,
    notification_rule_logs uuid     not null
        constraint notification_logs_notification_rules_logs
            references notification_rules
            on delete cascade
);
create unique index if not exists notificationlog_once
    on notification_logs (notifier_id, subject_id, offset_days, anchor_date, notification_rule_logs);
create index if not exists notificationlog_sent_at on notification_logs (sent_at);

-- +goose Down
drop table if exists notification_logs;
drop table if exists notification_rules;
alter table groups drop column alert_rules_seeded;
alter table groups drop column notify_hour;
