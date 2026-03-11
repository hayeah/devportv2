---
name: devportv2
description: Tmux-backed local process supervisor for TOML service specs. Use when you need to start, stop, inspect, or extend dev services with durable runtime state and blackbox lifecycle tests.
---

# devportv2

`devportv2` is a Go CLI for running local development services from a v2 TOML spec.
It launches one hidden supervisor per service inside tmux, persists runtime state in SQLite, and exposes a small CLI for lifecycle control and status inspection.

This README is written for coding agents and maintainers.
If you need the operational contract, also read [docs/runtime-invariants.md](./docs/runtime-invariants.md).

## What The Repo Contains

- `cli/devport/root.go`: Cobra CLI entry point and subcommand wiring.
- `manager.go`: main orchestration layer for `up`, `down`, `start`, `stop`, `restart`, `status`, `logs`, `freeport`, `ingress`, and hidden `supervise`.
- `supervisor.go`: child-process lifecycle, startup health gating, signal handling, and persistent state updates.
- `config.go`: TOML parsing, validation, normalization, and spec hashing.
- `store.go`: SQLite schema and persistence for services, health checks, and events.
- `tmux.go`: tmux session/window lifecycle and pane capture.
- `env.go`: env-file loading and `${VAR}` interpolation.
- `health.go`: process, HTTP, command, and no-op health probes.
- `e2e_test.go`: blackbox CLI happy-path coverage.
- `e2e_chaos_test.go`: blackbox chaos coverage that kills children, supervisors, and tmux windows to find stale-state bugs.

## Prerequisites

- Go `1.26`
- `tmux` on `PATH`
- a Unix-like environment with signals and process groups

The tests and the runtime both assume tmux is available.

## Mental Model

- One service spec maps to one tmux window.
- The visible CLI runs short-lived commands.
- The hidden `devport supervise` command is what actually owns the service lifecycle.
- The authoritative runtime view is reconciled from multiple sources:
  - desired config
  - SQLite `services` rows
  - flock-based supervisor locks
  - tmux windows
  - live child PID and bound port

Do not assume the database alone is truth. `status` intentionally reconciles stale rows when lock or process liveness disagrees.

## Config Model

Config defaults to `~/.config/devport/devport.toml`.
You can override it with `--file` or `DEVPORT_CONFIG`.

State defaults to `~/.local/share/devport`.
You can override it with `DEVPORT_STATE_DIR`.

Minimal example:

```toml
version = 2
port_range = { start = 19000, end = 19019 }
tmux_session = "devport"

[service."app/web"]
cwd = "~/src/myapp"
command = ["bin/web", "--port", "${PORT}"]
port = 19000
port_env = "APP_PORT"
env_files = [".env", ".env.local"]
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/healthz"
expect_status = [200]
startup_timeout = "10s"

[service."app/web".public]
hostname = "web.example.test"

[service."jobs/worker"]
cwd = "~/src/myapp"
command = ["bin/worker"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
startup_timeout = "5s"
```

Important validation rules:

- `version` must be `2`.
- each service must set exactly one of `port` or `no_port = true`.
- `port_env` requires `port`.
- `restart` only supports `"never"` today.
- `public.hostname` only makes sense for port-bearing services.
- duplicate service ports are rejected at config load time.
- every service needs a `health` block.

Supported health types:

- `none`
- `process`
- `http`
- `command`

Interpolation behavior:

- `${PORT}` is injected automatically for port-bearing services.
- `port_env` injects the same port value under a second env var name.
- `${env:NAME}` and `${NAME}` both expand from the merged environment map.
- later env files override earlier env files.
- env files overlay the current process environment rather than replacing it.

## Command Surface

- `devport up [--key ...]`
- `devport down [--key ...]`
- `devport start --key <service>`
- `devport stop --key <service>`
- `devport restart --key <service>`
- `devport status [--json] [--diff] [--key ...]`
- `devport logs --key <service> [--lines N]`
- `devport freeport [--key ...]`
- `devport ingress [--key ...]`
- `devport supervise --key <service>`
  - hidden command used internally from tmux

## Common Use Cases

```bash
# Scenario: start every service from the current spec.
go run ./cli/devport up

# Scenario: start only the HTTP app while leaving background jobs alone.
go run ./cli/devport start --key app/web

# Scenario: inspect machine-readable status from automation or tests.
go run ./cli/devport status --json

# Scenario: show only configuration/runtime drift without the full table.
go run ./cli/devport status --diff

# Scenario: restart a single service after changing code or env files.
go run ./cli/devport restart --key app/web

# Scenario: capture recent tmux pane output for a failing service.
go run ./cli/devport logs --key app/web --lines 300

# Scenario: ask devport for the next free port in the configured range.
go run ./cli/devport freeport

# Scenario: export public hostnames as JSON for an ingress adapter.
go run ./cli/devport ingress

# Scenario: stop everything and clean the tmux-backed runtime down.
go run ./cli/devport down
```

## Runtime Conventions

- tmux window names are deterministic hashes of service keys, not human-readable service names.
- tmux windows are created with `remain-on-exit on`, which helps log inspection after failures.
- selected env vars are pushed into tmux explicitly: `HOME`, `PATH`, `LOG_LEVEL`, `DEVPORT_STATE_DIR`, `DEVPORT_CONFIG`.
- logs go both to stderr and to `~/.local/log/devport.jsonl` through the local structured logger.
- the SQLite database lives at `<state-dir>/devport.db`.
- lock files live under `<state-dir>/locks`.

## Invariants And Recovery Rules

The short version:

- `running` and `starting` require the supervisor lock.
- `starting` is transient; a missing lock during startup means stale state.
- `status` may reconcile stale `running` or `starting` rows to `failed`.
- `stop` must be able to clean up even if the supervisor is already gone.
- `start` must reap stale orphan children before reusing the configured port.
- `up` is idempotent for services already active with a valid supervisor lock.

The detailed contract lives in [docs/runtime-invariants.md](./docs/runtime-invariants.md).

## Surprising Behavior

- `status` is not read-only in the purest sense. It may repair stale persisted state when runtime liveness disproves the DB row.
- A service in `failed` does not guarantee the child process is already gone. Recovery paths still need to check and reap stale PIDs.
- `health check failing` is drift, not automatically a state transition to `failed`.
- `wrong port listening` means the service is still running on the last recorded port while the config now wants a different one.
- `freeport` excludes both configured service ports and ports already listening on localhost.
- inside Go tests, `os.Executable()` points at the test binary, so tmux-backed manager tests must override `manager.executable` to the built CLI binary.

## Testing

Useful commands:

```bash
# Scenario: run the whole test suite, including tmux-backed blackbox tests.
go test ./...

# Scenario: rerun only the blackbox CLI coverage.
go test ./... -run 'TestEndToEnd|TestEndToEndChaosMonkey'

# Scenario: collect statement coverage across packages.
go test ./... -coverprofile=cover.out
go tool cover -func=cover.out
```

What the test suite already covers:

- config parsing and validation
- env interpolation and path resolution
- SQLite store behavior
- flock behavior
- health probe behavior
- manager lifecycle helpers
- tmux-backed integration lifecycle tests
- blackbox CLI E2E tests
- chaos-style blackbox process disruption tests

## When Editing This Repo

- preserve the v2 config contract unless you are intentionally versioning the spec
- update [docs/runtime-invariants.md](./docs/runtime-invariants.md) when lifecycle semantics change
- prefer blackbox tests for lifecycle changes
- if you touch recovery logic, extend `e2e_chaos_test.go` or `manager_integration_test.go`
- keep `supervise` internal; user-facing behavior should route through the normal commands
- be careful with stale-state fixes: database rows, locks, tmux windows, and live PIDs can disagree in partial failure cases

## Suggested Starting Points

- For CLI changes: start in `cli/devport/root.go`
- For spec or validation changes: start in `config.go`
- For lifecycle and recovery changes: start in `manager.go` and `supervisor.go`
- For state schema changes: start in `store.go`
- For failure analysis: read `docs/runtime-invariants.md`, then `e2e_chaos_test.go`
