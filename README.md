# Ethereum indexer

Foundation for a Go service that retains the latest canonical Ethereum mainnet
blocks, transactions, and raw EVM logs.

## Current scaffold

```text
cmd/indexer/            executable entry point
configs/                non-secret YAML defaults and examples
internal/config/        strict loading, environment overrides, validation
internal/domain/        storage-independent domain records
internal/ethereum/      narrow ethclient boundary
internal/indexer/       polling and reconciliation owner
internal/server/        Chi router and handler boundary
internal/service/       API query use cases
internal/store/         store contracts and flat adapter files
migrations/             PostgreSQL schema migrations
```

There is intentionally no `internal/app/app.go` package. The final executable
will compose dependencies directly in `cmd/indexer`, while `internal/server`
remains limited to HTTP routing and handlers.

The storage package is also intentionally flat:

```text
internal/store/store.go             interfaces
internal/store/postgres.go          pool lifecycle
internal/store/postgres_blocks.go   future block queries
internal/store/postgres_events.go   future event queries
```

A future Redis adapter, if it becomes justified, would be a separate flat file
such as `internal/store/redis.go`; it is not part of V1.

## Configuration

Copy `configs/config.example.yaml`, keep secrets out of the file, and provide:

```text
ETH_RPC_URL
DATABASE_URL
```

`CONFIG_FILE` changes the default path. The `-config` flag takes precedence.

The executable currently validates configuration, connects to Ethereum RPC,
verifies the chain ID, concurrently fetches the configured recent block window,
and stores it in memory. It then polls continuously, fetching only canonical
changes and retrying transient failures with bounded exponential backoff.
PostgreSQL and HTTP runtime wiring are intentionally left for later steps.

## Live RPC verification

Set a mainnet RPC URL without adding it to the configuration file:

```text
export ETH_RPC_URL="https://your-mainnet-provider.example"
go run ./cmd/indexer -config configs/config.example.yaml
```

The command fetches `indexer.block_window` blocks with at most
`ethereum.rpc_concurrency` concurrent block operations. It logs the range,
latest hash, and aggregate transaction and event counts, then waits for the
configured `indexer.poll_interval`. Stop it with Ctrl-C; SIGINT and SIGTERM
cancel active RPC work and retry waits before resources are closed. It does not
print the RPC URL or retain data after exit.

Repeated synchronization cycles reuse the stored canonical tip. An unchanged
head performs no block fetches, a canonical extension fetches only new blocks,
and a reorganization replaces blocks after the nearest common ancestor within
the retained window. If no retained ancestor exists, the current window is
fetched in full.

## Development checks

```text
make lint-install
make test
make vet
make lint
make lint-fix
make fmt
make check
```

`make lint-install` installs the pinned golangci-lint release into the ignored
`bin/` directory. `make lint` verifies the linter configuration, checks that
`go.mod` and `go.sum` are tidy, and runs the configured correctness, security,
resource-lifecycle, logging, and formatting checks. `make lint-fix` applies
supported fixes, while `make fmt` applies the configured gofmt and goimports
formatters.
