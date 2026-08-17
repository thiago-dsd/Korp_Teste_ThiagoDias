-- What a person is allowed to do.
--
-- Every authenticated user could do everything: adjust stock, print, delete
-- accounts. In a system that issues fiscal documents that is not a detail.
--
-- Two roles are enough here. An operator does the daily work — issuing and
-- printing invoices — and an administrator also changes the catalogue and the
-- balances, which are the operations that rewrite what the invoices are made
-- of. The first account created becomes an administrator, because a system
-- where nobody can manage the catalogue cannot be set up at all.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'operator';

ALTER TABLE users ADD CONSTRAINT users_role_valid CHECK (role IN ('operator', 'admin'));
