-- Give an existing database an administrator.
--
-- Roles arrived after accounts did, so every account already in the table took
-- the default and the system ended up with nobody able to manage the catalogue
-- — the exact situation the "first account administers" rule exists to avoid.
-- Adding the column is not enough; the accounts that predate it need a decision
-- too, and the oldest one is the closest thing to whoever set the system up.
--
-- On a fresh database the table is empty and this does nothing: there the first
-- registration becomes the administrator on its own.
UPDATE users
SET role = 'admin'
WHERE id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM users WHERE role = 'admin');
