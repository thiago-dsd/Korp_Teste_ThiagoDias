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

Filters combine freely and are applied by the database, never in memory:

| Products | |
| --- | --- |
| `search` | matches code and description |
| `min_balance`, `max_balance` | `max_balance=0` lists what is out of stock |
| `sort` | `code` (default) or `balance`, with `order=asc\|desc` |

| Invoices | |
| --- | --- |
| `status` | one or several: `status=OPEN,PRINTING` |
| `number` | finds a single invoice |
| `created_from`, `created_to` | a date covers the whole day |
| `product_id`, `product_code` | the invoices that used a product |
| `has_failure` | the ones whose last print attempt did not go through |
| `order` | `desc` (default) reads from the newest number |

An unusable filter is refused with `400 invalid_filter` naming every offending field at once, and
the listing keeps its filters while paging.

### Rate limiting

Operations are throttled by what they cost and by what abusing them achieves, not by one number
for the whole service:

| Category | Default | Counted per | Why |
| --- | --- | --- | --- |
| Read | 300/min | signed in person | Cheap, and a screen open on a listing makes many calls |
| Write | 60/min | signed in person | Touches the database and the broker |
| Assistant | 20/min | signed in person | Paid for on every call |
| Bulk | 10/min | signed in person | One call can be a hundred writes |
| Sign in | 30/min | address | Generous on purpose; see below |
| Public floor | 600/min | address | Bounds unauthenticated traffic |

Authenticated traffic is counted per person rather than per address, so an office behind one
address does not share a single allowance. Health probes, the endpoints the services call on each
other and the published key set are never throttled: monitoring polls constantly, and throttling
the internal calls would mean the system limiting its own printing flow.

Guessing passwords is stopped by an account lockout rather than by the address limit: ten failed
attempts close that account for fifteen minutes, counted in the database so every instance sees
the same number, and applied to addresses with no account as well so the answer never reveals who
is registered. A colleague on the same address keeps working the whole time.

Every refusal is the usual error envelope with `429`, a `Retry-After` and the `RateLimit-*`
headers, and says nothing about how the limit works. Limits are configured per environment (see
`.env.example`); the allowance is per instance, so a deployment with several replicas divides
them.

### Bulk actions

Three operations are worth doing to many resources at once, and they do not all behave the same
way, because what a failure means differs:

| Endpoint | Items | On failure |
| --- | --- | --- |
| `POST /products/bulk` | Independent | The good rows are created; the bad ones say why |
| `POST /products/adjustments` | One document | Nothing is changed at all |
| `POST /invoices/print` | Independent | The accepted invoices keep printing |

Importing a catalogue with one malformed line should bring the rest in, so those items stand on
their own. A delivery note is the opposite: applying half of it leaves the operator unsure what
landed, and sending it again would count the applied half twice, so every movement is applied in
one transaction or none is.

Adjustments are signed movements (`delta`), never a new balance. Setting a balance would silently
undo whatever happened between reading it and writing it — an invoice printed in that moment, for
instance — while adding to it never loses that. The database does the arithmetic and refuses to go
below zero, and products are touched in the same order the debit of an invoice uses, so an
adjustment and a print cannot deadlock each other. Repeating a delivery note is stopped by the
usual `Idempotency-Key`.

Printing and adjusting are offered on the screens: invoices are picked from the listing and
printed together, and products are picked and moved in one document. What was applied leaves the
selection and what was refused stays, with the reason next to it, so the operator corrects and
sends again. Importing a catalogue is API only.

Every call takes at most 100 items and answers with the same shape: whether the items stand or
fall together, a count to read first, and one compact line per item carrying its position, what
happened and why. Positions are what line the answer up with the request, so nothing has to be
matched on values. The status code says which case it is — `200` when everything went through,
`207` when the fates differed, `409` when an atomic call was rolled back — and the results are
identities and reasons rather than whole entities, so a hundred items stay readable.

### Failure scenarios

| Scenario | What happens |
| --- | --- |
| Stock service is down | Creating an invoice answers `503 stock_unavailable`; reading invoices keeps working |
| Broker is down | Print requests stay in the outbox and are published when it returns, however long it takes; services reconnect on their own |
| Broker is down for a long time | Nothing is discarded: a message in the outbox is work that was already committed, so it keeps being retried and is reported in the logs once it has been failing long enough to need a person |
| A late answer arrives after a retry | Each print carries an attempt number. A rejection only applies to the attempt that produced it, so an answer held back by the broker cannot cancel the print that is running now |
| A late confirmation arrives | It still closes the invoice: the stock records the debit per invoice, so the balances are gone whichever request got the answer through |
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
