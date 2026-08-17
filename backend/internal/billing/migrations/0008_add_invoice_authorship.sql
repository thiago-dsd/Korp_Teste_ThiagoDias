-- Who issued the invoice and who printed it.
--
-- An invoice is a fiscal document and it had no author: the service knew
-- exactly which person was making the request and stored none of it, so there
-- was no way to answer "who issued this?" after the fact. The email is a
-- snapshot for the same reason the product code is one — deleting an account
-- must not erase who signed a document that was already issued.
ALTER TABLE invoices
    ADD COLUMN created_by_id    UUID,
    ADD COLUMN created_by_email TEXT,
    ADD COLUMN printed_by_id    UUID,
    ADD COLUMN printed_by_email TEXT;

-- Answering "what did this person issue?" without reading every invoice.
CREATE INDEX invoices_created_by_idx ON invoices (created_by_id, number DESC);
