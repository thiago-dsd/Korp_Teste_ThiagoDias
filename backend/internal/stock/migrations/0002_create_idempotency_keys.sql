-- Idempotency records for write endpoints: a repeated request carrying the
-- same Idempotency-Key replays the stored response instead of doing the work
-- twice.
CREATE TABLE idempotency_keys (
    endpoint      TEXT        NOT NULL,
    key           TEXT        NOT NULL,
    request_hash  TEXT        NOT NULL,
    status_code   INTEGER,
    response_body BYTEA,
    reserved_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ,

    PRIMARY KEY (endpoint, key)
);

-- Supports cleaning up old records.
CREATE INDEX idempotency_keys_reserved_at_idx ON idempotency_keys (reserved_at);
