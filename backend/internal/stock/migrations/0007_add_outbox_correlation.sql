-- The id of the request that caused the event.
--
-- Work that crosses the queue leaves the request that started it: an invoice is
-- printed here and debited by another service seconds later. Carrying the
-- correlation id with the event is what lets both halves of that story be found
-- in the logs with one search.
ALTER TABLE outbox_messages ADD COLUMN correlation_id TEXT NOT NULL DEFAULT '';
