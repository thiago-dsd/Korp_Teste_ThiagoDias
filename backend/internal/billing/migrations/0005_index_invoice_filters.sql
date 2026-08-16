-- Listing by status walks the invoices from the newest number, so the index
-- carries both columns in the order they are read.
CREATE INDEX invoices_status_number_idx ON invoices (status, number DESC);

-- Filtering by the day an invoice was issued.
CREATE INDEX invoices_created_at_idx ON invoices (created_at DESC);

-- Invoices whose last print attempt failed are a small slice of the table, so
-- a partial index keeps it small and the lookup direct.
CREATE INDEX invoices_failed_idx ON invoices (number DESC) WHERE failure_code IS NOT NULL;

-- Finding the invoices that used a product looks the item up by product, the
-- opposite direction of the existing index.
CREATE INDEX invoice_items_product_idx ON invoice_items (product_id);
CREATE INDEX invoice_items_product_code_idx ON invoice_items (upper(product_code));
