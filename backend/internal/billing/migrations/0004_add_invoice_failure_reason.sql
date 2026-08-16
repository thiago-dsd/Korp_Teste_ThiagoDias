-- When printing fails, the invoice goes back to OPEN carrying the reason, so
-- the operator sees what went wrong instead of a silent status change.
ALTER TABLE invoices
    ADD COLUMN failure_code    TEXT,
    ADD COLUMN failure_message TEXT,
    ADD COLUMN printing_since  TIMESTAMPTZ;

-- Supports finding invoices stuck in PRINTING for too long.
CREATE INDEX invoices_printing_since_idx
    ON invoices (printing_since)
    WHERE status = 'PRINTING';
