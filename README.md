# Stockly

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

Two roles decide what an account may do. An **operator** reads the catalogue and issues and
prints invoices, which is the daily work. An **administrator** also changes the catalogue and the
balances — the operations that rewrite what invoices are made of. The role travels inside the
signed token, so each service enforces it without asking identity on every request, and a refusal
answers `403` rather than `401`: signing in again fixes one and cannot fix the other.

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

### Searching for invoices by writing

The listing screen takes a question in plain words — *"notas fechadas de agosto com martelo"* — and
answers with **filters, not invoices**. `POST /invoices/search` returns a filter set; the screen
writes it into the controls above the table and into the URL, and the ordinary listing endpoint
reads it from there.

That indirection is the whole design. What the assistant understood is visible on screen, editable
by hand, survives a reload and can be sent to a colleague as a link — none of which is true of an
answer produced directly by a model.

Two things guard it. The filters go through `ParseQuery`, the same function that reads a hand-typed
query string, so **a filter the listing would reject is a filter the assistant cannot produce**. And
a product is resolved against the real catalogue, because the listing matches a product code
exactly and an invented one would quietly return an empty page instead of an answer.

One bad filter drops itself rather than the whole question: asking for open invoices in a month the
model spelled wrong still narrows to the open ones, and says what it left out.

#### Turning it on

The assistant is off unless it is configured, and it says so rather than failing: `GET
/invoices/draft` answers `{"available": false}`, the new-invoice screen does not render the field
at all, and invoices are written by hand. Nothing else degrades.

It needs a chat deployment reachable over the classic Azure OpenAI API — `POST
{endpoint}/openai/deployments/{deployment}/chat/completions`, authenticated with an `api-key`
header. Put the three values in a `.env` file at the root of the repository, which `docker compose`
reads and `.gitignore` already excludes:

```
AZURE_AI_FOUNDRY_ENDPOINT=https://<account>.openai.azure.com
AZURE_AI_FOUNDRY_API_KEY=<key>
AZURE_AI_FOUNDRY_DEPLOYMENT=<deployment name>
AZURE_AI_FOUNDRY_API_VERSION=2024-10-21
```

Then `docker compose --profile services up -d billing-service` to pick them up.

Provisioning the smallest thing that works, from scratch:

```sh
az provider register -n Microsoft.CognitiveServices   # new subscriptions need this

az group create -n rg-stockly-ai -l eastus2

az cognitiveservices account create -n <globally-unique-name> -g rg-stockly-ai -l eastus2 \
  --kind OpenAI --sku S0 --custom-domain <globally-unique-name> --yes

az cognitiveservices account deployment create -n <name> -g rg-stockly-ai \
  --deployment-name gpt-4o --model-name gpt-4o --model-version 2024-11-20 \
  --model-format OpenAI --sku-name Standard --sku-capacity 10

az cognitiveservices account show -n <name> -g rg-stockly-ai --query properties.endpoint -o tsv
az cognitiveservices account keys list -n <name> -g rg-stockly-ai --query key1 -o tsv
```

No Key Vault, storage account or Foundry hub: the service talks to the endpoint directly with a
key, so anything else would be cost without use. `S0` has no standing charge and a Standard
deployment bills per token, so an idle environment costs nothing. `az group delete -n
rg-stockly-ai --yes` removes all of it.

**Choosing the model matters more than it looks.** The client sends `temperature` and `max_tokens`
and asks for `response_format: json_object`. The reasoning models (`o1`, `o3`, `gpt-5`) reject
`temperature` and expect `max_completion_tokens` instead, so they fail against this client without
a code change. `gpt-4o-mini` would be the cheaper pick but its only version, `2024-07-18`, was
deprecated on 2026-03-31 and Azure refuses new deployments of it. `gpt-4o` is the cheapest model
that both takes these parameters and still has Standard quota.

Check quota before provisioning — a fresh subscription often has none for a given model in a given
region:

```sh
az cognitiveservices usage list -l eastus2 \
  --query "[?contains(name.value,'Standard.gpt-4o')].{quota:name.value, limit:limit}" -o table
```

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

On the screens the filters live in the query string, so a filtered listing survives a reload, walks
back with the browser, and can be sent to a colleague as a link — `/products?stock=low` and
`/invoices?attention=true` are what the dashboard tiles point at. Either listing exports what is on
screen, filters included, as a CSV.

### Stock history

A balance answers how much there is; it cannot answer why. `GET /products/{id}/movements` returns
the changes to one product, newest first, paged by cursor like every other listing:

```
GET /products/{id}/movements   ->  { "items": [ { "delta": -3, "balance_after": 7,
                                                  "source": "invoice", ... } ], "next_cursor": "" }
```

Every change writes a row: the opening balance a product is registered with, a correction typed on
the form, a delivery or a stock count, and stock taken by an invoice being printed. The row is
written **in the same transaction as the balance change it describes**, so the history can never
disagree with the balance it explains — a refused adjustment leaves no trace, and a print request
delivered twice records one movement, for the same reason it debits once.

A movement caused by a person keeps their address; one caused by an invoice points at the invoice,
which already records who printed it. Nothing here is ever updated or deleted: a correction is
another movement. Products registered before this existed simply have no history, and the screen
says so rather than claiming nothing ever moved.

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

All three are offered on the screens. Invoices are picked from the listing and printed together;
products are picked and moved in one document; and a catalogue is imported by pasting rows from a
spreadsheet, which is the only way a real catalogue ever gets in. The paste accepts comma,
semicolon and tab, ignores a header row, and shows what it understood before anything is written.
What was applied leaves the selection and what was refused stays, with the reason next to it, so
the operator corrects and sends again.

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

Each service reports what it is doing on `GET /internal/metrics`, in the format Prometheus
scrapes: traffic by method and status, how many events are waiting in the outbox, how many have
been failing long enough to need a person, and how many messages were dead lettered.

When a message is given up on it lands in a dead letter queue, which is visible and replayable
without opening the broker UI:

```bash
curl localhost:8082/internal/dead-letters -H "X-Service-Token: $SERVICE_TOKEN"
curl -X POST localhost:8082/internal/dead-letters/replay \
  -H "X-Service-Token: $SERVICE_TOKEN" -H 'Content-Type: application/json' \
  -d '{"queue":"billing.stock_results","limit":50}'
```

Replaying keeps the message id, so anything that was in fact already applied is recognised and
skipped rather than done twice, and whatever still fails goes back to the dead letter queue
instead of being lost.

The stock service can be made to fail on purpose, which is useful for a demonstration:

```bash
curl -X POST localhost:8081/internal/failure-simulation \
  -H 'Content-Type: application/json' \
  -H "X-Service-Token: $SERVICE_TOKEN" \
  -d '{"enabled":true}'
```

## Technical write-up

The document the challenge asks for — Angular lifecycle hooks, RxJS, libraries, Go dependency
management and frameworks, error handling — is in
[docs/detalhamento-tecnico.md](docs/detalhamento-tecnico.md), written in Portuguese for the
people evaluating it.

## Performance

Load scenarios, measured results, the one optimisation the data justified and the budgets that
catch regressions are in [docs/performance.md](docs/performance.md). The load generator lives in
`backend/cmd/loadgen`.

## Running it

You need **Docker** and **Node.js 22+**. Everything else runs in containers.

```bash
git clone https://github.com/thiago-dsd/Korp_Teste_ThiagoDias.git
cd Korp_Teste_ThiagoDias

make services-up                          # database, broker and the three services
cd frontend && npm install && npm start   # the app, on http://localhost:4200
```

The first command builds three images and starts them with PostgreSQL and RabbitMQ. Each service
creates and migrates its own database on startup, so there is nothing to seed by hand. It takes a
couple of minutes the first time and seconds after that.

Then open <http://localhost:4200> and **create an account** — registration is open, and the account
can register products right away.

A first pass through the system, in the order the challenge describes it:

1. **Products → New product.** Code, description and balance — say `MART-01`, a hammer, 10 units.
2. **Invoices → New invoice.** Pick the product, quantity 2, and create it. It gets a sequential
   number and opens as `OPEN`.
3. **Print it.** The status goes to `PRINTING` while the stock service is asked, then to `CLOSED`.
4. **Back to Products.** The balance is now 8.

Everything answers on these ports:

| | |
| --- | --- |
| App | <http://localhost:4200> |
| Stock service | <http://localhost:8081> |
| Billing service | <http://localhost:8082> |
| Identity service | <http://localhost:8083> |
| RabbitMQ management | <http://localhost:15672> — `korp` / `korp` |
| PostgreSQL | `localhost:5433` — `korp` / `korp` |

`make services-down` stops everything and keeps the data. `make infra-reset` throws the databases
away and starts over.

### Turning on the assistants

The two AI features are off until a model is configured, and the screens simply do not offer them.
To switch them on, put an Azure AI Foundry deployment in a `.env` file at the root — the format and
the `az` commands that provision one are under
[Writing an invoice with the assistant](#writing-an-invoice-with-the-assistant) above — and run
`make services-up` again.

Nothing else needs a `.env`: the compose file carries the development defaults, and the services
migrate and start without one.

### Working on the backend

To run the Go services on the host instead of in containers, with **Go 1.26+** installed:

```bash
make infra-up        # only PostgreSQL and RabbitMQ, in containers
make run-identity    # :8083
make run-stock       # :8081
make run-billing     # :8082
```

Each in its own terminal. The Makefile passes the configuration inline, so there is still no `.env`
to prepare.

The services build from one multi-stage `backend/Dockerfile` parameterised by which command to
compile, and run as a non-root user from a ~30 MB image with nothing in it but the static binary
and the certificates needed to reach the AI endpoint. The Angular app runs with `npm start` either
way, because its API URLs are baked in at build time and pointing them at containers is a
deployment decision rather than a development one.

## Tests

```bash
make check-backend             # gofmt, go vet and Go unit tests
make test-backend-integration  # adds the tests that need a real PostgreSQL
make test-frontend             # Angular unit tests
cd frontend && npm run lint    # ESLint with the Angular and accessibility rules
```

Integration tests are skipped when `TEST_DATABASE_URL` is not set, so the unit suite runs
anywhere.

## Screens

| Screen | What it is for |
| --- | --- |
| **Today** | What needs attention before anything else gets printed: invoices that failed, invoices still open, what is out of stock and what is running out. Each number links to the listing already filtered for it. |
| **Products** | The catalogue, with a balance read as a state rather than a number — out, running low, fine. Register one, correct one, import a whole catalogue, move balances in one document, and read why any balance is what it is. |
| **Invoices** | Every invoice and its state, filtered by status, number, period, product or "needs attention", and printed in batches. |
| **Invoice** | One document: what happened to it, in order, with who issued it and who printed it, and the button that prints it. |

The landing page used to explain what the application is for, which is worth reading exactly once.
An operator opening this system already knows; what they do not know is which invoices failed
overnight and what is about to run out, and both were three clicks and two filters away.

Nothing counts the whole table to fill a tile: the listings are paged by cursor and carry no total
on purpose, so a full page is reported as `20+` rather than as `20`. Each panel is read
independently, and one service being slow leaves that panel empty and says so, instead of leaving
the page blank.

## The mark

The Korp initial: a circle set against an angular stem, with the counter between them left open so
the background reads through it. It comes from `logo-initial.svg` and is used unchanged — same path,
same proportions. It is a single silhouette with one counter, which is what lets it hold together
small, and why there is no separate small-size variant to keep in step.

Two rules. **Size it by height and let the width follow** (the viewBox is 1129 × 1125, very nearly
but not exactly square — constraining both turns the circle into an ellipse). **Give it no padding
of its own**: the empty top-left and bottom-right corners are part of the drawing and already
provide the breathing room.

Its red is `--brand: #f12d2d`, which is deliberately *not* `--primary`. The primary colour is a
preference — the operator can turn the whole application violet or green from the profile menu —
and a logo that changes colour with a theme is a different logo. This is also why the mark no longer
sits on a `bg-primary` tile in the navigation: on a rose theme it disappeared into the tile, and on
a blue one it fought with it. It now stands in brand red directly on the surface, and reverses to
white only where it sits on a filled panel (the favicon, the sign in artwork).

| File | Use |
| --- | --- |
| `src/assets/icons/logo.svg` | The mark, in `currentColor`. Everywhere in the interface — sidebar, navbar and sign in. |
| `src/favicon.svg` | The mark reversed to white on a brand-red tile. A bare silhouette on transparency does not hold against both a light and a dark browser chrome. |
| `src/favicon.ico` | 16, 32 and 48, each rendered at its own pixel size rather than downsampled, for browsers that ignore SVG favicons. |
| `src/apple-touch-icon.png` | 180×180, square and opaque: iOS applies its own rounded mask, and transparent corners come out black. |
| `src/assets/brand/logo-512.png` | 512×512 master, for anything outside the application. |
| `src/assets/brand/logo-initial.svg` | The mark as delivered, untouched. Everything above is derived from it. |

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
