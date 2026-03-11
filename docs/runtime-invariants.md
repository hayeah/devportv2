# Runtime Invariants

This document describes the runtime invariants `devportv2` relies on today.
They are the contract between the config, SQLite state, tmux supervision, lock files, and the CLI.
The blackbox lifecycle tests in `e2e_test.go` and `e2e_chaos_test.go` exercise these invariants against real processes.

## Runtime Sources

- Config spec: the desired service definition, including command, cwd, health check, and desired port.
- `services` row in SQLite: the persisted runtime view for each service.
- Supervisor lock file: the liveness marker for the active supervisor process.
- tmux window: the terminal container that launched the hidden `supervise` command.
- Child PID and bound port: the actual process state on the host.

No single source is sufficient on its own. `status` and lifecycle commands reconcile across them.

## State Invariants

### `running`

- SQLite status is `running`.
- Supervisor lock is held.
- Service PID is non-zero and expected to be alive.
- tmux window exists.
- If the service has a port, that port should be listening.
- Health may still be `unhealthy` if the health probe fails; that is reported as drift, not an automatic state transition.

### `starting`

- SQLite status is `starting`.
- Supervisor lock must be held.
- This state is transitional and should converge quickly to `running` or `failed`.
- A `starting` row without the supervisor lock is stale and must be treated as failed startup.

### `stopped`

- SQLite status is `stopped`.
- Supervisor lock is not held.
- Supervisor PID is zero.
- Service PID should be zero after a normal stop path.
- The tmux window should be gone after explicit stop/down.

### `failed`

- SQLite status is `failed`.
- Supervisor lock is not held.
- Supervisor PID is zero.
- `last_error` or `last_reason` should explain the failure path when possible.
- A failed row means the previous runtime is not trusted.

`failed` is intentionally weaker than `stopped`. If the supervisor died unexpectedly, an orphan child process may still exist briefly. Recovery commands must not assume that `failed` implies the child is already gone.

## Reconciliation Rules

### Status Reconciliation

`status` is allowed to repair obviously stale runtime state.

- If SQLite says `running` or `starting` but the supervisor lock is gone, the service is reconciled to `failed`.
- That reconciliation records drift `supervisor lock not held`.
- Health is saved as unhealthy for that condition.
- `last_reason` is set to `supervisor_missing` when filling in the failed row.

This keeps the CLI from reporting a healthy/running service when the supervisor has already disappeared.

### Start Recovery

`start` and `up` must be safe to run after partial crashes.

- If the supervisor lock is already held, start fails with `already running`.
- If a stale child PID is still alive while the supervisor lock is gone, start must reap that process group before launching a new supervisor.
- Start must clear stale runtime rows before waiting for the new supervisor to report `starting` or `running`.
- `up` is idempotent for services already in `running` or `starting` with the supervisor lock held.

Without this, `up` can fail on `port already in use` or report an old failure from a previous crashed supervisor.

### Stop Recovery

`stop` must be able to clean up even if the supervisor is already dead.

- If the supervisor lock is held and the supervisor PID exists, stop sends `SIGTERM` to the supervisor and waits for the lock to drop.
- If the child PID is still alive after that, or if the lock was already gone, stop kills the child process group directly.
- Stop then removes the tmux window and persists `stopped` with zero PIDs.

This makes `stop` a reliable cleanup operation, not just a polite request to the supervisor.

## Drift Semantics

Drift is diagnostic. It does not always mean the service must be restarted immediately.

- `spec changed since last start`: the stored spec hash does not match the current config.
- `wrong port listening`: the service is running on a recorded port that no longer matches the config.
- `port not listening`: the service claims to be active on a port but the socket is not accepting connections.
- `health check failing`: the runtime process exists but the configured health probe is failing.
- `supervisor lock not held`: persisted runtime state said the service was active, but supervisor liveness disproved it.

## Test Scope

The current blackbox tests assert these invariants through real CLI calls and real OS process behavior:

- happy-path lifecycle
- restart PID changes
- failed startup behavior
- spec drift reporting
- supervisor death
- child death
- tmux window loss
- repeated `up/start/stop/restart`
- randomized config mutation and port conflicts

If lifecycle behavior changes, update this document and the blackbox tests together.
