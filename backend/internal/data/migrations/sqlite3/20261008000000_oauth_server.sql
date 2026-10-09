-- +goose Up
-- Built-in OAuth 2.1 authorization server for MCP clients (AI assistants).
--   oauth_clients   registered applications (public clients; PKCE, no secret)
--   oauth_requests  authorization requests awaiting the user's consent (minutes)
--   oauth_grants    a user's standing consent: client + collection + scopes
--   oauth_tokens    authorization codes, access and refresh tokens (hashed)
create table if not exists oauth_clients
(
    id            uuid         not null
        primary key,
    created_at    datetime     not null,
    updated_at    datetime     not null,
    client_id     varchar(64)  not null,
    name          varchar(255) not null,
    redirect_uris json         not null
);

create unique index if not exists oauth_clients_client_id_key
    on oauth_clients (client_id);

create index if not exists oauthclient_client_id
    on oauth_clients (client_id);

create table if not exists oauth_grants
(
    id                  uuid     not null
        primary key,
    created_at          datetime not null,
    updated_at          datetime not null,
    scopes              json     not null,
    last_used_at        datetime,
    group_id            uuid     not null
        constraint oauth_grants_groups_oauth_grants
            references groups
            on delete cascade,
    oauth_client_grants uuid     not null
        constraint oauth_grants_oauth_clients_grants
            references oauth_clients
            on delete cascade,
    user_id             uuid     not null
        constraint oauth_grants_users_oauth_grants
            references users
            on delete cascade
);

create index if not exists oauthgrant_user_id on oauth_grants (user_id);
create index if not exists oauthgrant_group_id on oauth_grants (group_id);

create table if not exists oauth_requests
(
    id                    uuid           not null
        primary key,
    created_at            datetime       not null,
    updated_at            datetime       not null,
    redirect_uri          varchar(2048)  not null,
    scopes                json           not null,
    state                 varchar(2048),
    code_challenge        varchar(128)   not null,
    resource              varchar(2048),
    expires_at            datetime       not null,
    oauth_client_requests uuid           not null
        constraint oauth_requests_oauth_clients_requests
            references oauth_clients
            on delete cascade
);

create index if not exists oauthrequest_expires_at
    on oauth_requests (expires_at);

create table if not exists oauth_tokens
(
    id                 uuid          not null
        primary key,
    created_at         datetime      not null,
    updated_at         datetime      not null,
    kind               text          not null
        check (kind in ('code', 'access', 'refresh')),
    token_hash         blob          not null,
    expires_at         datetime      not null,
    used_at            datetime,
    code_challenge     varchar(128),
    redirect_uri       varchar(2048),
    oauth_grant_tokens uuid          not null
        constraint oauth_tokens_oauth_grants_tokens
            references oauth_grants
            on delete cascade
);

create unique index if not exists oauth_tokens_token_hash_key
    on oauth_tokens (token_hash);

create index if not exists oauthtoken_token_hash on oauth_tokens (token_hash);
create index if not exists oauthtoken_expires_at on oauth_tokens (expires_at);

-- +goose Down
drop table if exists oauth_tokens;
drop table if exists oauth_requests;
drop table if exists oauth_grants;
drop table if exists oauth_clients;
