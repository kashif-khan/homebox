-- +goose Up
-- Built-in OAuth 2.1 authorization server for MCP clients (AI assistants).
--   oauth_clients   registered applications (public clients; PKCE, no secret)
--   oauth_requests  authorization requests awaiting the user's consent (minutes)
--   oauth_grants    a user's standing consent: client + collection + scopes
--   oauth_tokens    authorization codes, access and refresh tokens (hashed)
CREATE TABLE IF NOT EXISTS "oauth_clients" (
    "id"            uuid NOT NULL,
    "created_at"    timestamptz NOT NULL,
    "updated_at"    timestamptz NOT NULL,
    "client_id"     character varying(64) NOT NULL,
    "name"          character varying(255) NOT NULL,
    "redirect_uris" jsonb NOT NULL,
    PRIMARY KEY ("id")
);
CREATE UNIQUE INDEX IF NOT EXISTS "oauth_clients_client_id_key" ON "oauth_clients" ("client_id");
CREATE INDEX IF NOT EXISTS "oauthclient_client_id" ON "oauth_clients" ("client_id");

CREATE TABLE IF NOT EXISTS "oauth_grants" (
    "id"                  uuid NOT NULL,
    "created_at"          timestamptz NOT NULL,
    "updated_at"          timestamptz NOT NULL,
    "scopes"              jsonb NOT NULL,
    "last_used_at"        timestamptz NULL,
    "group_id"            uuid NOT NULL,
    "oauth_client_grants" uuid NOT NULL,
    "user_id"             uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "oauth_grants_groups_oauth_grants" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "oauth_grants_oauth_clients_grants" FOREIGN KEY ("oauth_client_grants") REFERENCES "oauth_clients" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "oauth_grants_users_oauth_grants" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "oauthgrant_user_id" ON "oauth_grants" ("user_id");
CREATE INDEX IF NOT EXISTS "oauthgrant_group_id" ON "oauth_grants" ("group_id");

CREATE TABLE IF NOT EXISTS "oauth_requests" (
    "id"                    uuid NOT NULL,
    "created_at"            timestamptz NOT NULL,
    "updated_at"            timestamptz NOT NULL,
    "redirect_uri"          character varying(2048) NOT NULL,
    "scopes"                jsonb NOT NULL,
    "state"                 character varying(2048) NULL,
    "code_challenge"        character varying(128) NOT NULL,
    "resource"              character varying(2048) NULL,
    "expires_at"            timestamptz NOT NULL,
    "oauth_client_requests" uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "oauth_requests_oauth_clients_requests" FOREIGN KEY ("oauth_client_requests") REFERENCES "oauth_clients" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "oauthrequest_expires_at" ON "oauth_requests" ("expires_at");

CREATE TABLE IF NOT EXISTS "oauth_tokens" (
    "id"                 uuid NOT NULL,
    "created_at"         timestamptz NOT NULL,
    "updated_at"         timestamptz NOT NULL,
    "kind"               character varying NOT NULL,
    "token_hash"         bytea NOT NULL,
    "expires_at"         timestamptz NOT NULL,
    "used_at"            timestamptz NULL,
    "code_challenge"     character varying(128) NULL,
    "redirect_uri"       character varying(2048) NULL,
    "oauth_grant_tokens" uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "oauth_tokens_kind_check" CHECK ("kind" IN ('code', 'access', 'refresh')),
    CONSTRAINT "oauth_tokens_oauth_grants_tokens" FOREIGN KEY ("oauth_grant_tokens") REFERENCES "oauth_grants" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "oauth_tokens_token_hash_key" ON "oauth_tokens" ("token_hash");
CREATE INDEX IF NOT EXISTS "oauthtoken_token_hash" ON "oauth_tokens" ("token_hash");
CREATE INDEX IF NOT EXISTS "oauthtoken_expires_at" ON "oauth_tokens" ("expires_at");

-- +goose Down
DROP TABLE IF EXISTS "oauth_tokens";
DROP TABLE IF EXISTS "oauth_requests";
DROP TABLE IF EXISTS "oauth_grants";
DROP TABLE IF EXISTS "oauth_clients";
