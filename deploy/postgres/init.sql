-- Each service owns its own database: they never share tables.
-- The *_test databases are used by the Go integration tests.
CREATE DATABASE stock;
CREATE DATABASE billing;
CREATE DATABASE stock_test;
CREATE DATABASE billing_test;
CREATE DATABASE messaging_test;
