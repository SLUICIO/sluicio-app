-- OAuth 2.1 authorization server for the remote MCP endpoint (docs/mcp.md).
-- Clients self-register via Dynamic Client Registration (Claude's connector
-- does this); authorization codes are short-lived + single-use. Access tokens
-- are not stored here — the token endpoint mints a viewer-scoped api_token, so
-- the MCP endpoint's existing Bearer auth + RBAC validate them unchanged.

CREATE TABLE oauth_clients (
    client_id     TEXT PRIMARY KEY,
    client_name   TEXT        NOT NULL DEFAULT '',
    redirect_uris TEXT[]      NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE oauth_auth_codes (
    code                  TEXT PRIMARY KEY,
    client_id             TEXT        NOT NULL,
    redirect_uri          TEXT        NOT NULL,
    code_challenge        TEXT        NOT NULL DEFAULT '',
    code_challenge_method TEXT        NOT NULL DEFAULT 'S256',
    user_id               UUID        NOT NULL,
    scope                 TEXT        NOT NULL DEFAULT '',
    expires_at            TIMESTAMPTZ NOT NULL,
    consumed              BOOLEAN     NOT NULL DEFAULT false,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oauth_auth_codes_expires ON oauth_auth_codes (expires_at);
