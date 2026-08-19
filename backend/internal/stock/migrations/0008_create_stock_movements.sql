-- Every change to a balance, in the order it happened.
--
-- The balance column of a product answers "how much is there"; it cannot
-- answer "why". Without this table an administrator who finds a balance of 7
-- has no way of telling a delivery from a correction from an invoice that was
-- printed, which is the first question asked whenever a count does not match.
--
-- A row is written in the same transaction as the balance change it describes,
-- so the history can never disagree with the balance it explains.
CREATE TABLE stock_movements (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id    UUID        NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- How much the balance moved. Negative takes stock out.
    delta         INTEGER     NOT NULL,
    -- What the balance became, so a row explains itself without replaying the
    -- whole history.
    balance_after INTEGER     NOT NULL,
    -- What caused it: registration, edit, adjustment or invoice.
    source        TEXT        NOT NULL,
    -- Free note the operator wrote on a delivery or a correction.
    reason        TEXT        NOT NULL DEFAULT '',
    -- Set when the cause was an invoice being printed.
    invoice_id    UUID,
    -- Snapshot of who did it. Deleting an account cannot erase who moved
    -- stock, the same reason an invoice keeps its author as text.
    actor_email   TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT stock_movements_balance_not_negative CHECK (balance_after >= 0),
    CONSTRAINT stock_movements_source_known
        CHECK (source IN ('registration', 'edit', 'adjustment', 'invoice'))
);

-- The history is always read for one product, newest first, and paged by
-- keyset on (created_at, id).
CREATE INDEX stock_movements_product_idx
    ON stock_movements (product_id, created_at DESC, id DESC);
