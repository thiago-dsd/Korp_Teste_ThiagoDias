-- One row per invoice whose balances were debited. The primary key is what
-- makes the debit idempotent: a repeated print request for the same invoice
-- can never take the balance twice.
CREATE TABLE stock_debits (
    invoice_id UUID        PRIMARY KEY,
    debited_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
