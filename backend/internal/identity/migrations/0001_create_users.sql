-- Accounts able to sign in. Passwords are never stored, only their argon2id
-- hash, and the address is unique regardless of casing.
CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT users_email_not_blank CHECK (btrim(email) <> ''),
    CONSTRAINT users_email_length    CHECK (char_length(email) <= 254),
    CONSTRAINT users_name_not_blank  CHECK (btrim(name) <> ''),
    CONSTRAINT users_name_length     CHECK (char_length(name) <= 120)
);

CREATE UNIQUE INDEX users_email_unique_idx ON users (lower(email));

-- Refresh tokens, stored as hashes and grouped in families.
--
-- A family is one sign in: every rotation stays in the same family, so if a
-- stolen token is replayed the whole family can be revoked at once, ending
-- both the attacker's session and the victim's.
CREATE TABLE refresh_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id   UUID        NOT NULL,
    token_hash  TEXT        NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    user_agent  TEXT        NOT NULL DEFAULT '',

    CONSTRAINT refresh_tokens_expires_after_issue CHECK (expires_at > issued_at)
);

CREATE UNIQUE INDEX refresh_tokens_hash_unique_idx ON refresh_tokens (token_hash);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);
