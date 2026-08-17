-- Every change to a product bumps this number, so a write can say which state
-- it was based on.
--
-- Editing a product sends the whole product back, including the balance the
-- form was filled with. While that form is open an invoice can be printed and
-- debit the balance; saving the form would then write the old value back and
-- put stock that was already sold into the catalogue again. The debit itself is
-- careful (balance = balance - quantity), and this is what stops a plain edit
-- from undoing it: the write only lands if nothing changed in between.
ALTER TABLE products ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
