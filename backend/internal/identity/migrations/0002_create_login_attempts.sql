-- Failed sign in attempts per address being tried.
--
-- Throttling by network address does nothing against an attacker spread over
-- many addresses working through the passwords of one account, so the count
-- that matters lives in the database, where every instance of the service sees
-- the same number.
CREATE TABLE login_attempts (
    email          TEXT        PRIMARY KEY,
    failures       INTEGER     NOT NULL DEFAULT 0,
    first_failure  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_failure   TIMESTAMPTZ NOT NULL DEFAULT now(),
    blocked_until  TIMESTAMPTZ
);

-- Supports clearing out attempts nobody cares about anymore.
CREATE INDEX login_attempts_last_failure_idx ON login_attempts (last_failure);
