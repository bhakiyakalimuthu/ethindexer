# Ethereum Mainnet Indexer

A small Go service that continuously indexes the latest canonical Ethereum
mainnet blocks into PostgreSQL and exposes read-only HTTP APIs for blocks,
transactions, and raw EVM logs.

The V1 service intentionally retains only the configured recent block window,
with a maximum of 50 blocks. It is designed as a focused indexer rather than a
complete archive node or blockchain explorer.

## What is included

- Ethereum mainnet polling through JSON-RPC using go-ethereum/ethclient.
- Configurable latest, safe, or finalized head selection.
- Concurrent block and log fetching through a bounded worker pool.
- Storage of blocks, all transaction records, transaction input, and raw logs.
- Canonical-chain reconciliation and retained-window reorganization handling.
- Atomic PostgreSQL writes, retention pruning, migrations, constraints, and
  query indexes.
- Bounded PostgreSQL batches of at most 1,000 statements inside one transaction.
- Chi HTTP API with request IDs, panic recovery, request timeouts, and JSON
  errors.
- Zerolog structured logging.
- Dependency-free liveness and PostgreSQL-backed readiness endpoints.
- Graceful HTTP, indexer, RPC, and database shutdown on SIGINT or SIGTERM.
- Unit tests and an opt-in PostgreSQL integration suite.
- A non-root, read-only, distroless application image and a local Docker
  Compose stack.

Not included in V1:

- Historical indexing beyond the retained window.
- Transaction receipts, execution status, or effective gas price.
- Internal calls, traces, or internal ETH transfers.
- ABI event decoding or indexing addresses embedded in event topics.
- Redis, Kubernetes, Prometheus, or OpenTelemetry.

## What the service does

On startup the service:

1. Loads and validates YAML configuration plus supported environment overrides.
2. Connects to PostgreSQL and Ethereum JSON-RPC.
3. Verifies that the RPC endpoint reports Ethereum mainnet chain ID 1.
4. Selects the configured chain head.
5. Fetches the most recent configured block window concurrently.
6. Stores the canonical blocks, transactions, raw logs, and synchronization
   state in one PostgreSQL transaction.
7. Starts polling for canonical changes while serving the HTTP API.

Every fetched block requires:

- One full block request, including its transactions.
- One block-hash-filtered log request for all raw EVM logs in that block.

On later polls, an unchanged head performs no block fetch. A normal extension
fetches only new blocks. On a reorganization, the indexer searches the retained
window for the nearest common ancestor and atomically replaces the divergent
suffix. If no retained ancestor exists, it reloads the complete current window.

## Architecture

```mermaid
flowchart LR
    RPC["Ethereum mainnet<br/>JSON-RPC"]
    Caller["API client"]
    DB[("PostgreSQL")]

    subgraph Process["eth-indexer process"]
        Main["cmd/indexer<br/>composition and lifecycle"]
        Client["Ethereum adapter<br/>ethclient and per-call timeout"]
        Syncer["Indexer syncer<br/>poll, retry, reorg, retention"]
        Workers["Fetcher worker pool<br/>blocks, transactions, logs"]
        HTTP["Chi server<br/>middleware, routes, handlers"]
        Services["Query and health services"]
        Contracts["Store interfaces"]
        Postgres["PostgreSQL adapter"]
    end

    Main --> Client
    Main --> Syncer
    Main --> HTTP
    Syncer --> Workers
    Syncer --> Client
    Workers --> Client
    Client <--> RPC
    Syncer --> Contracts
    Caller --> HTTP
    HTTP --> Services
    Services --> Contracts
    Contracts --> Postgres
    Postgres <--> DB
```

The dependency direction for HTTP reads is:

```text
server handler -> service use case -> store interface -> PostgreSQL adapter
```

The server package contains only HTTP routing, middleware, request parsing,
handlers, and response mapping. Application construction remains in
cmd/indexer. The store package stays flat: store.go contains contracts, while
PostgreSQL lifecycle, writes, scans, and individual query areas live in
separate files.

## Project layout

```text
cmd/indexer/             process construction, logger, runtime, shutdown
configs/                 example and container YAML configuration
internal/config/         strict configuration loading and validation
internal/domain/         storage-independent records and limits
internal/ethereum/       narrow ethclient adapter and RPC timeouts
internal/indexer/        fetching, worker pool, sync, retry, reorg logic
internal/server/         Chi router, handlers, middleware, HTTP models
internal/service/        query and readiness use cases
internal/store/          interfaces, in-memory store, PostgreSQL adapter
migrations/              PostgreSQL up and down migrations
compose.yaml             local PostgreSQL, migration, and indexer services
Dockerfile               multi-stage non-root distroless image
```

## Storage model

| Table | Purpose |
| --- | --- |
| blocks | Canonical block header fields and indexing timestamp |
| transactions | Transaction identity, position, sender, recipient, fees, value, and input |
| event_logs | Raw EVM log address, topics, data, and canonical position |
| sync_state | Chain ID and last successful canonical synchronization |

Foreign keys use cascading deletes so a replaced or pruned block removes its
transactions and logs. Event queries use an index on emitter address plus
descending canonical position. Transaction-log lookup has a separate index.

Canonical updates use a transaction-scoped PostgreSQL advisory lock. It
serializes cooperating indexer writers but does not take a table lock or block
ordinary API reads. All deletes, inserts, and sync-state changes commit or roll
back together.

## Requirements

Recommended Docker setup:

- Docker with Compose V2.
- An Ethereum mainnet HTTPS JSON-RPC endpoint.
- curl and jq for verification.

Native development setup:

- Go 1.25 or newer.
- PostgreSQL 17.
- An Ethereum mainnet JSON-RPC endpoint.
- The migrate CLI, or Docker for applying migrations.

Never commit a real RPC URL when it contains a provider token.

## Quick start with Docker

1. Create the local environment file:

```bash
cp .env.example .env
```

2. Edit .env and replace the placeholder with a real mainnet endpoint:

```text
ETH_RPC_URL=https://your-mainnet-provider.example
HTTP_PORT=8080
POSTGRES_PORT=5432
LOG_LEVEL=info
```

3. Build and start PostgreSQL, migrations, and the indexer:

```bash
make docker-up
```

Docker Compose waits for PostgreSQL to become healthy, runs migrations, and
starts the indexer only after migration completes successfully.

4. Follow synchronization logs:

```bash
make docker-logs
```

A successful cycle logs the retained range, selected head, block count,
transaction count, event count, and synchronization timestamp. The RPC URL is
not logged.

5. Inspect container state when needed:

```bash
docker compose ps --all
docker compose logs migrate
```

The migrate container exiting with status 0 is expected.

6. Stop the stack:

```bash
make docker-down
```

The postgres_data volume is retained by the normal shutdown command.

## Run natively

The following option uses the Compose PostgreSQL service while running Go on
the host:

```bash
docker compose up --detach postgres
docker compose run --rm migrate

export ETH_RPC_URL="https://your-mainnet-provider.example"
export DATABASE_URL="postgres://indexer:indexer@127.0.0.1:5432/indexer?sslmode=disable"

go run ./cmd/indexer -config configs/config.example.yaml
```

Stop the process with Ctrl-C. The signal cancels active RPC calls and retry
waits, gracefully shuts down HTTP, and then closes the RPC and database clients.

## Configuration

The service loads configs/config.example.yaml by default. CONFIG_FILE changes
the default path, and the -config argument takes precedence:

```bash
CONFIG_FILE=configs/config.example.yaml go run ./cmd/indexer
go run ./cmd/indexer -config configs/config.example.yaml
```

Supported value overrides:

| Environment variable | Overrides |
| --- | --- |
| ETH_RPC_URL | ethereum.rpc_url |
| DATABASE_URL | database.url |
| LOG_LEVEL | app.log_level |

Main settings:

| Setting | Example default | Purpose |
| --- | ---: | --- |
| app.environment | development | Selects development console or JSON logging |
| app.log_level | info | Zerolog minimum level |
| http.address | :8080 | HTTP listen address |
| http.request_timeout | 5s | Maximum handler duration |
| http.shutdown_timeout | 15s | Graceful HTTP shutdown allowance |
| ethereum.chain_id | 1 | Required Ethereum mainnet chain ID |
| ethereum.rpc_timeout | 30s | Timeout applied independently to each RPC call |
| ethereum.rpc_concurrency | 4 | Maximum concurrent block operations |
| indexer.poll_interval | 4s | Delay between successful poll cycles |
| indexer.block_window | 50 | Retained block count; V1 accepts 1 through 50 |
| indexer.head_mode | latest | Head selector: latest, safe, or finalized |
| indexer.retry_attempts | 3 | Retries after a failed synchronization attempt |
| indexer.retry_min_backoff | 1s | Initial synchronization retry delay |
| indexer.retry_max_backoff | 10s | Maximum synchronization retry delay |
| database.max_connections | 12 | PostgreSQL pool maximum |
| database.min_connections | 2 | PostgreSQL pool minimum |
| database.connect_timeout | 10s | Initial PostgreSQL connection timeout |

The 30-second RPC timeout is per operation, not for the entire 50-block range.
If a hosted provider throttles the initial load, first reduce
ethereum.rpc_concurrency to 2 or 1. Keep RPC and database credentials in
environment variables or a secret manager rather than YAML committed to Git.

## HTTP API

All responses are JSON. Hashes, addresses, calldata, topics, and log data use
0x-prefixed hexadecimal strings. Uint256 value and fee fields are returned as
decimal strings so they do not lose precision in JSON clients.

| Method and path | Description |
| --- | --- |
| GET /healthz | Process liveness |
| GET /readyz | Database and indexed-data readiness |
| GET /v1/blocks/{number} | Block plus all transaction hashes |
| GET /v1/transactions/{hash} | Transaction plus all raw logs for that transaction |
| GET /v1/addresses/{address}/events | Raw logs emitted by an address |

### Liveness

```http
GET /healthz
```

Returns 200 as long as the HTTP process can serve requests. It deliberately
does not call PostgreSQL or Ethereum RPC.

```json
{"status":"ok"}
```

### Readiness

```http
GET /readyz
```

Returns 200 after PostgreSQL is reachable and at least one canonical block has
been stored:

```json
{"status":"ready","last_indexed_block":25835188}
```

It returns 503 with status not_ready during the initial sync, if migrations are
missing, or when PostgreSQL cannot serve the required queries. It does not call
Ethereum RPC for every readiness request.

### Block by number

```http
GET /v1/blocks/25835188
```

The number must be an unsigned decimal integer. The response contains block
metadata and transaction hashes ordered by transaction index. A valid block
outside the retained window returns 404.

### Transaction by hash

```http
GET /v1/transactions/0xea6e8c92ffb22f9cdac5b5bf6378e3a2d835422f8ad8f5946fda6160507cea2a
```

The hash must be an exact 0x-prefixed 32-byte hexadecimal value. The response
contains transaction fields and raw logs ordered by log index.

For EIP-1559 type-2 transactions, gas_price_wei is intentionally null while
max_fee_per_gas_wei and max_priority_fee_per_gas_wei are populated. An empty
events array means the transaction emitted no raw EVM logs; it does not mean
that execution traces or internal transfers were inspected.

### Events emitted by address

```http
GET /v1/addresses/0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48/events?limit=5
```

The address is matched against the EVM log emitter field, log.address. It does
not match transaction sender or recipient fields and does not decode addresses
embedded in topics or data.

Results are ordered newest first.

| Query parameter | Default | Rules |
| --- | ---: | --- |
| limit | 100 | Integer from 1 through 1,000 |
| cursor | absent | Opaque next_cursor returned by the previous response |

If a reorganization makes a cursor non-canonical, the endpoint returns 409
stale_cursor. Restart pagination without the old cursor.

### Errors

Errors do not expose database or RPC details:

```json
{
  "error": {
    "code": "transaction_not_found",
    "message": "transaction was not found",
    "request_id": "request-id"
  }
}
```

Common statuses are 400 for malformed input, 404 for data not present in the
retained window, 409 for a stale event cursor, 500 for an internal query
failure, and 503 for failed readiness.

## Verify the running service

Set the base URL:

```bash
BASE_URL=http://127.0.0.1:8080
```

### 1. Verify liveness and readiness

```bash
curl -sS "$BASE_URL/healthz" | jq
curl -sS "$BASE_URL/readyz" | jq
```

Health should return immediately. Readiness becomes successful after the first
canonical window commits.

### 2. Obtain real retained identifiers

These commands read one current block, transaction, and event-emitter address
from the local PostgreSQL container:

```bash
BLOCK_NUMBER=$(docker compose exec -T postgres \
  psql -U indexer -d indexer -Atc \
  "SELECT number FROM blocks ORDER BY number DESC LIMIT 1")

TX_HASH=$(docker compose exec -T postgres \
  psql -U indexer -d indexer -Atc \
  "SELECT '0x' || encode(hash, 'hex')
   FROM transactions
   ORDER BY block_number DESC, transaction_index DESC
   LIMIT 1")

EVENT_ADDRESS=$(docker compose exec -T postgres \
  psql -U indexer -d indexer -Atc \
  "SELECT '0x' || encode(emitter_address, 'hex')
   FROM event_logs
   ORDER BY block_number DESC, log_index DESC
   LIMIT 1")
```

EVENT_ADDRESS can be empty when none of the currently retained transactions
emitted a log.

### 3. Query a block

```bash
curl -sS "$BASE_URL/v1/blocks/$BLOCK_NUMBER" | jq
```

The returned transaction_count should equal the length of transaction_hashes.

### 4. Query a transaction

```bash
curl -sS "$BASE_URL/v1/transactions/$TX_HASH" | jq
```

Its block_number and block_hash should agree with the corresponding block
response, and its hash should appear at transaction_hashes[index].

### 5. Query address events

When EVENT_ADDRESS is non-empty:

```bash
curl -sS --get \
  "$BASE_URL/v1/addresses/$EVENT_ADDRESS/events" \
  --data-urlencode "limit=5" | jq
```

To request the next page, copy next_cursor from the first response:

```bash
curl -sS --get \
  "$BASE_URL/v1/addresses/$EVENT_ADDRESS/events" \
  --data-urlencode "limit=5" \
  --data-urlencode "cursor=PASTE_NEXT_CURSOR_HERE" | jq
```

## Development and tests

Run the complete local check:

```bash
make check
```

This runs:

- golangci-lint configuration validation and linting.
- A go.mod and go.sum tidiness check.
- go vet.
- All unit tests.

Install the pinned linter once if needed:

```bash
make lint-install
```

Individual commands:

```bash
make test
make vet
make lint
make lint-fix
make fmt
```

Run the PostgreSQL integration suite against a disposable or development
database. The tests create and remove their own isolated schemas:

```bash
TEST_DATABASE_URL="postgres://indexer:indexer@127.0.0.1:5432/indexer?sslmode=disable" \
  go test -tags=integration ./internal/store
```

Do not point the integration suite at a database account that is not allowed to
create and drop test schemas.

## Operational notes

- The initial synchronization must succeed within its retry policy; otherwise
  the service exits and Docker can restart it.
- Later synchronization failures are logged and retried on the next polling
  cycle while already indexed reads remain available.
- RPC workers stop promptly when the parent context is canceled or a range
  fetch fails.
- Large writes are split into batches of at most 1,000 SQL statements, limiting
  client-side batch memory while preserving one atomic database transaction.
- Empty Ethereum calldata and empty log data are stored as empty bytea values,
  not SQL NULL.
- Normal API reads are not blocked by the advisory writer lock.
- Removing old blocks and reorg suffixes uses foreign-key cascades for
  transactions and logs.

## Troubleshooting

### Ethereum context deadline exceeded

The timeout applies separately to each block or log request. Hosted RPC plans
can slow down or throttle when several full blocks are requested concurrently.

1. Confirm ethereum.rpc_timeout is at least 30s.
2. Reduce ethereum.rpc_concurrency from 4 to 2 or 1.
3. Check the provider dashboard for throttling or request limits.
4. Restart with make docker-up so the container is rebuilt with configuration
   changes.

### Readiness returns 503

- During startup, wait for the first canonical window to commit.
- Check docker compose logs indexer for synchronization failures.
- Check docker compose logs migrate and confirm exit status 0.
- Confirm PostgreSQL is healthy with docker compose ps --all.

### A block or transaction returns 404

V1 stores only the latest configured window. A previously successful lookup can
return 404 after the chain advances beyond that window or after a
reorganization.

### PostgreSQL reports a required bytea value as NULL

Current code normalizes empty transaction input and log data before PostgreSQL
encoding. Rebuild the application image to ensure an older container is not
running:

```bash
make docker-up
```

An insert failure during ApplyCanonicalUpdate is an application data-write
failure, not necessarily a migration failure. The canonical update is
transactional and rolls back completely.

## Security notes

- Treat tokenized RPC URLs as credentials and rotate them if exposed in logs,
  shell history, screenshots, or messages.
- Do not reuse the fixed local Compose database password in production.
- Bind production ports and database access according to the deployment
  environment rather than the local loopback defaults.
- The runtime image is non-root, distroless, read-only, and configured with
  no-new-privileges.

## AI-assisted development

See [AI_USAGE.md](AI_USAGE.md) for the tools and model disclosure, shareable
configuration, session excerpts, approximate human/AI authorship, and the
verification process used for generated code.
