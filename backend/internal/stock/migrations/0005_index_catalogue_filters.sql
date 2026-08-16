-- Searching the catalogue uses ILIKE '%term%', which no plain btree index can
-- serve. The trigram index makes that search use an index instead of reading
-- every product, which is what keeps the listing fast once the catalogue grows.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX products_code_search_idx ON products USING gin (code gin_trgm_ops);
CREATE INDEX products_description_search_idx ON products USING gin (description gin_trgm_ops);

-- Ordering by balance is how "what is running out" is asked for; the code is
-- part of the index because it is the tiebreaker the cursor pages on.
CREATE INDEX products_balance_idx ON products (balance, upper(code));
