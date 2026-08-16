# Invoice System

Invoice issuing system built for the Korp technical challenge: product registration,
invoice creation with sequential numbering, and invoice printing that closes the invoice and
debits the stock balances.

## Architecture

```
Angular SPA (frontend/)
   |  HTTP/JSON, carrying the access token
   +--> identity-service  accounts and tokens           -> PostgreSQL (identity)
   +--> stock-service     products and balances         -> PostgreSQL (stock)
   +--> billing-service   invoices and printing         -> PostgreSQL (billing)
                 \                                  /
                  +----------> RabbitMQ <----------+
                        events published from each
                        service's transactional outbox
```

- Two independent services, each owning its own database. No shared tables.
- Printing is asynchronous: billing marks the invoice as `PRINTING` and records the event in its
  outbox in the same transaction; stock debits the balances and answers with an event; billing
  closes the invoice or reopens it with the reason for the failure.
- Retries are safe: message consumption and stock debits are idempotent.
- Products and invoices are only served to a signed in user. Tokens are signed by the identity
  service with an RSA key and verified by the others through its published public key, so no
  other service is able to mint one.

### Accounts and sessions

```
POST /auth/register, /auth/login   ->  access token (15 min) + refresh token (7 days)
POST /auth/refresh                 ->  rotates the refresh token and issues a new pair
POST /auth/logout                  ->  ends that session only
GET  /auth/me, DELETE /auth/me     ->  profile and account deletion (password confirmed)
GET  /.well-known/jwks.json        ->  public key the other services verify tokens with
```

Passwords are stored as argon2id hashes. Refresh tokens are stored hashed and rotated on every
use: replaying one that was already exchanged revokes the whole session, which is what limits the
damage when a token leaks. Deleting an account removes its sessions with it.

### Printing an invoice

```
POST /invoices/{id}/print   ->  202 Accepted, status PRINTING
   billing: status = PRINTING + invoice.print_requested in the outbox (one transaction)
   stock:   debits every balance atomically + answers through its own outbox
              success  -> stock.debited   -> billing: status = CLOSED
              rejected -> stock.rejected  -> billing: status = OPEN + reason shown to the user
   no answer within 2 minutes -> billing reopens the invoice explaining the timeout
```

Only invoices with status `OPEN` can be printed, and an invoice being printed is locked, so two
operators clicking at the same time produce a single debit request.

### Writing an invoice with the assistant

The billing service can turn a sentence such as *"two steel bolts and a hammer"* into invoice
lines, using a chat deployment on Microsoft Azure AI Foundry.

The model only suggests. Every code it answers with is resolved against the real catalogue,
quantities must be positive integers within the allowed range, repeated products are merged and
anything that does not match is dropped with a warning shown to the operator. The invoice is
created by the regular endpoint, after the person reviews the lines. A sentence trying to talk the
model into ignoring its instructions therefore cannot reach the stock: the rules are enforced
after the answer comes back, not by the prompt.

Set `AZURE_AI_FOUNDRY_ENDPOINT`, `AZURE_AI_FOUNDRY_API_KEY` and `AZURE_AI_FOUNDRY_DEPLOYMENT` to
enable it. Without them the screen does not offer the assistant and invoices are written by hand.

### Listings

`GET /products` and `GET /invoices` answer one page at a time:

```
GET /products?limit=20&search=bolt        ->  { "items": [...], "next_cursor": "..." }
GET /products?limit=20&cursor=<next_cursor>   the following page
```

Paging is done by cursor rather than by offset. The page is cut by the value of the column the
listing is ordered by, so a product registered while someone is paging never makes an item show up
twice or disappear, and reading page fifty costs the same as reading page one. The cursor is
opaque: clients pass back what they received. Pages hold 20 items by default and 100 at most.

### Failure scenarios

| Scenario | What happens |
| --- | --- |
| Stock service is down | Creating an invoice answers `503 stock_unavailable`; reading invoices keeps working |
| Broker is down | Print requests stay in the outbox and are published when it returns; services reconnect on their own |
| Not enough balance | Nothing is debited, the invoice returns to `OPEN` with `insufficient_balance` |
| Stock never answers | The invoice is reopened after two minutes explaining the timeout |
| Repeated request | An `Idempotency-Key` replays the first answer; a redelivered event never debits twice |
| Access token expired | The application refreshes the session and replays the request without interrupting anyone |
| Refresh token replayed | The session is revoked on the spot and both the attacker and the client have to sign in again |
| Assistant unavailable or answering nonsense | The answer is discarded and the invoice is written by hand |

The stock service can be made to fail on purpose, which is useful for a demonstration:

```bash
curl -X POST localhost:8081/internal/failure-simulation \
  -H 'Content-Type: application/json' \
  -H "X-Service-Token: $SERVICE_TOKEN" \
  -d '{"enabled":true}'
```

## Requirements

- Go 1.26+
- Node.js 22+ and npm
- Docker (PostgreSQL and RabbitMQ)

## Getting started

```bash
cp .env.example .env     # development defaults, adjust if needed
make infra-up            # PostgreSQL on :5433 and RabbitMQ on :5672 (UI on :15672)
make run-identity        # identity-service on :8083
make run-stock           # stock-service on :8081
make run-billing         # billing-service on :8082
cd frontend && npm install && npm start   # Angular app on :4200
```

Services apply their own database migrations at startup.

## Tests

```bash
make check-backend             # gofmt, go vet and Go unit tests
make test-backend-integration  # adds the tests that need a real PostgreSQL
make test-frontend             # Angular unit tests
```

Integration tests are skipped when `TEST_DATABASE_URL` is not set, so the unit suite runs
anywhere.

## Layout

```
backend/
  cmd/stock-service      service entry point
  cmd/billing-service    service entry point
  cmd/identity-service   service entry point
  internal/stock         stock domain, storage and migrations
  internal/billing       billing domain, storage and migrations
  internal/identity      accounts, passwords and tokens
  internal/config        environment configuration
  internal/platform      shared building blocks (HTTP, PostgreSQL, errors, logging, health)
frontend/                Angular application
deploy/                  local infrastructure files
```

## Configuration

Every service reads its configuration from the environment; see `.env.example`.
`DATABASE_URL`, `RABBITMQ_URL` and `SERVICE_TOKEN` are mandatory and the service refuses to start
without them.

## License

Released under the [MIT License](LICENSE).
