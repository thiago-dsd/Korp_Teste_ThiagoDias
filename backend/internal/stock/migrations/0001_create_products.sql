-- Products owned by the stock service. Balance can never go negative: the
-- database constraint is the last line of defence for concurrent debits.
CREATE TABLE products (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT        NOT NULL,
    description TEXT        NOT NULL,
    balance     INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT products_code_not_blank        CHECK (btrim(code) <> ''),
    CONSTRAINT products_code_length           CHECK (char_length(code) <= 32),
    CONSTRAINT products_description_not_blank CHECK (btrim(description) <> ''),
    CONSTRAINT products_description_length    CHECK (char_length(description) <= 200),
    CONSTRAINT products_balance_not_negative  CHECK (balance >= 0)
);

-- Product codes are unique regardless of casing, so "p-1" and "P-1" cannot
-- both exist and confuse the operator.
CREATE UNIQUE INDEX products_code_unique_idx ON products (upper(code));
