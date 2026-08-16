-- Transactional outbox: events are written in the same transaction as the
-- state change that produced them, and published to the broker afterwards.
CREATE TABLE outbox_messages (
    id              UUID        PRIMARY KEY,
    -- Gives the relay a total order to publish in, which timestamps alone
    -- cannot guarantee when two events share the same microsecond.
    sequence        BIGSERIAL   NOT NULL,
    type            TEXT        NOT NULL,
    aggregate_id    TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT
);

-- The relay only ever looks for messages that are still pending.
CREATE INDEX outbox_messages_pending_idx
    ON outbox_messages (next_attempt_at, sequence)
    WHERE published_at IS NULL;

-- Messages already handled by a consumer. Delivery is at-least-once, so a
-- redelivered message must not be applied twice.
CREATE TABLE processed_messages (
    consumer     TEXT        NOT NULL,
    message_id   UUID        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (consumer, message_id)
);
