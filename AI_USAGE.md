# AI usage disclosure

This repository was developed collaboratively by the human author and OpenAI
Codex. Codex produced many first drafts, tests, and review suggestions; the
human author set the architecture, repeatedly changed generated code, reviewed
incremental results, and verified the service against Ethereum mainnet.

This document describes the tools used, the shareable configuration, examples
from the working sessions, approximate module ownership, and the checks used to
judge correctness.

## Tools and models

| Tool | Version or model | How it was used |
| --- | --- | --- |
| OpenAI Codex desktop task | GPT-5-based Codex coding agent; the exact backend snapshot was not exposed by the client | Architecture discussion, incremental Go implementation, code review, tests, PostgreSQL schema work, Docker setup, debugging, and documentation |
| ChatGPT macOS client hosting the Codex task | 26.810.52044, build 6662, observed when this disclosure was prepared | Conversation, local workspace access, patch application, and terminal-driven verification |
| Go toolchain | Go 1.26.1 locally; the module targets Go 1.25 | Formatting, compilation, unit tests, vet, and module checks |
| golangci-lint | v2.12.2, pinned by the Makefile | Static analysis, formatting verification, security checks, and logging checks |
| Docker and PostgreSQL | Docker 29.4.0 and PostgreSQL 17 container plus Docker Compose | Migration verification, isolated storage integration tests, and local end-to-end operation |
| curl and jq | Local command-line tools | Human-run verification of live block and transaction API responses |
| Git | Local repository | Incremental review and commits controlled by the human author after requesting that Codex not commit |

The observable desktop-client version above is not a model version. The task
identified the model only as a GPT-5-based Codex agent, so this document does
not invent a more specific model identifier.

The locally installed Codex CLI was not used to generate or edit this project.
Claude Code, Cursor, GitHub Copilot, MCP servers, external plugins, custom
skills, hooks, slash commands, and subagents were not used in these sessions.

## Shareable configuration

The repository configuration used during implementation is committed alongside
the code:

- .golangci.yml pins the lint and formatting policy used to review generated
  and manually changed Go code.
- Makefile provides the repeatable test, vet, lint, format, and Docker entry
  points used during development.
- configs/config.example.yaml and configs/config.container.yaml document the
  non-secret runtime configuration.
- .env.example documents local environment variables without including a real
  provider credential.
- Dockerfile and compose.yaml capture the reproducible build, PostgreSQL,
  migration, and local runtime setup.

No project-specific Claude, Cursor, or Codex configuration file drove the
implementation. The existing AGENTS.md contained only workspace-mirror
instructions and no ethindexer architecture or coding conventions, so it is
not presented as an AI-development configuration.

The following working conventions were supplied interactively by the human
author and then reflected in the repository structure:

- Work in small, reviewable increments.
- Keep cmd/indexer responsible for construction and lifecycle.
- Return errors from run and keep exit behavior at the main boundary.
- Keep internal/server limited to routing, middleware, handlers, and HTTP
  models.
- Put query contracts and use cases in internal/service.
- Keep internal/store flat, with interfaces in store.go and adapter behavior in
  separate files.
- Reuse the fetcher and other long-lived dependencies.
- Add focused tests with each increment.
- Do not add Redis, Kubernetes, Prometheus, or telemetry in V1.
- Do not commit after the human author requested manual review and commits.

No private global Codex configuration, hidden prompt library, MCP connection, or
third-party account was required for the work. Normal platform safety and
workspace instructions are not reproduced here.

## Collaboration method

Work was deliberately split into small increments:

1. The human author reviewed the design and requested one small implementation
   step at a time.
2. Codex inspected the current repository before each change and proposed or
   implemented the bounded increment.
3. Unit tests were added beside the affected package.
4. The human author reviewed or rewrote the result before moving to the next
   increment.
5. Higher-risk storage changes were checked with PostgreSQL integration tests.
6. The human author tested the assembled service with a production Ethereum
   RPC endpoint and queried the resulting HTTP APIs.

The human author explicitly asked Codex not to commit subsequent work, so later
changes were left for human review, staging, commit, and push.

## Session excerpts

These excerpts are shortened to avoid provider credentials and routine terminal
output. They show the prompts and observable outcomes, not private model
reasoning.

### A prompt that produced a useful verification boundary

Human:

```text
lets have in-memory store to verify the previous changes, will configure the
prod rpc endpoint to fetch infos and test, before moving on to the next
incremental change
```

Outcome:

- Codex added the in-memory store and tests.
- The fetcher and RPC mapping could be exercised without PostgreSQL.
- The human author then confirmed that the live mainnet endpoint worked before
  authorizing the synchronization and PostgreSQL increments.

This was useful because it separated RPC correctness from database correctness
instead of debugging both at once.

### A human rewrite followed by an AI review loop

Human:

```text
i have implemented ranger.go myself instead of range.go. go through the
ranger.go and validate and add more test for the same
```

Outcome:

- The human author wrote the concurrent range worker implementation.
- Codex reviewed cancellation, ordering, worker limits, invalid ranges, and
  canonical parent validation.
- Codex added focused tests rather than replacing the human implementation.

This is representative of the collaboration: generated code was not treated as
the only acceptable implementation.

### A generated bug, production evidence, and recovery

The first generated transaction mapping copied bytes with:

```go
Input: append([]byte(nil), transaction.Data()...),
```

For an Ethereum transaction with empty calldata, that expression produced a nil
slice. pgx encoded the nil slice as SQL NULL, but the migration correctly
defined transactions.input as NOT NULL.

Human-provided production evidence:

```text
null value in column "input" of relation "transactions" violates not-null
constraint
```

Recovery:

- Codex identified that the failure occurred during the canonical data write,
  not during migration.
- Empty transaction input, empty log data, and empty topic collections were
  normalized while mapping Ethereum records.
- The PostgreSQL adapter also added a required-byte normalization boundary so a
  future caller cannot reintroduce SQL NULL accidentally.
- Unit regression tests were added for empty transaction and event data.
- The PostgreSQL integration suite was run against an isolated PostgreSQL 17
  container and passed.
- The temporary database container was removed after the test.

The same report contained an RPC URL with a provider token. It is deliberately
redacted here, and credential rotation was recommended.

## Approximate authorship and review

The ownership below describes first drafts and meaningful rewrites rather than
claiming that every line has a single author.

| Area | Codex contribution | Human contribution |
| --- | --- | --- |
| Initial structure and domain | Drafted the package skeleton, configuration types, domain records, store contracts, and initial migration | Chose the flat layout and rejected an internal/app/app.go composition package |
| Main and logging | Drafted executable construction and graceful runtime | Removed the separate logging package, moved logger construction into main, and requested the run-returning-error pattern instead of broad os.Exit usage |
| Server and service boundaries | Drafted Chi handlers, response models, service implementations, and tests | Renamed router to server and moved query interfaces from the router layer into service |
| Ethereum adapter | Drafted the narrow ethclient wrapper, chain validation, timeouts, and tests | Supplied and ran the real mainnet RPC verification |
| Block fetcher | Drafted block, transaction, sender, fee, and raw-log mapping plus validation tests | Changed construction so one fetcher instance is reused and updated main and tests |
| Range worker pool | Reviewed behavior and expanded tests | Authored ranger.go and chose its filename and implementation |
| Synchronization | Drafted polling, retained-window reconciliation, common-ancestor handling, retries, and tests | Requested reusable errors, head-mode constants, and incremental review before further changes |
| In-memory storage | Drafted the adapter and tests | Requested it specifically as a checkpoint before PostgreSQL work and verified the result live |
| PostgreSQL | Drafted schema, indexes, canonical transaction, bounded batches, advisory locking, scans, queries, and integration tests | Challenged batch memory and locking behavior, reviewed the explanations, and exercised the implementation with real indexed data |
| HTTP APIs | Drafted block, transaction, address-event, health, and readiness endpoints and tests | Verified transaction/block consistency using curl and jq and clarified expected outputs |
| Tooling and containers | Drafted Make targets, lint policy, Dockerfile, Compose stack, and migration service | Set the scope, deferred telemetry, requested no Redis/Kubernetes/Prometheus, and controlled commits |
| Reliability fixes and docs | Diagnosed empty-byte SQL NULL handling, tuned conservative RPC defaults, added regressions, and expanded documentation | Supplied production failure logs, reran the service, and confirmed successful endpoint responses |

The human author also decided when each increment was acceptable and repeatedly
requested smaller changes instead of accepting a single large generated
implementation.

## How correctness was assessed

Generated code was not accepted solely because it compiled or because
AI-generated tests passed.

The review process included:

- Human inspection and manual rewrites of architectural boundaries and runtime
  construction.
- Focused unit tests for configuration, Ethereum mapping, worker cancellation
  and ordering, synchronization, storage, handlers, and graceful shutdown.
- make check, which runs module tidiness checks, golangci-lint, go vet, and all
  unit tests.
- PostgreSQL integration tests that create and drop isolated schemas and cover
  canonical replacement, retention pruning, constraints, query behavior, and
  bounded batch flushing.
- A specific integration regression for nil Ethereum calldata and log data.
- Live Ethereum mainnet synchronization using a human-supplied RPC endpoint.
- Manual HTTP checks for block lookup, transaction lookup, transaction
  position, block hash consistency, and transaction-count consistency.
- Review of failure behavior: RPC cancellation and retry, atomic PostgreSQL
  rollback, graceful signal handling, and stale pagination cursors.

At the final recorded check, lint, vet, unit tests, and the PostgreSQL
integration suite passed. The human also observed successful mainnet block and
transaction responses.

## Remaining limits

This process does not replace an independent security audit, sustained load
test, provider-failure test, or long-running reorganization test. V1 also does
not validate execution receipts or traces because they are outside the stated
requirements.
