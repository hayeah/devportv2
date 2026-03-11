---
name: devport
description: Manage dev services with stable port assignment, health checks, and tmux-backed process supervision. Use when you need to start, stop, inspect, or restart dev services on a shared machine.
---

# devport

`devport` is a local dev service manager. It runs services in tmux, tracks state in SQLite, and reports drift between desired config and actual runtime.

All services are declared in one TOML spec file. No ad-hoc unnamed services, no implicit port assignment, no hidden auto-restart.

## Config File

Default location: `~/.config/devport/devport.toml`
Override with `--file <path>` or `DEVPORT_CONFIG` env var.

```toml
version = 2
port_range = { start = 19000, end = 19999 }
tmux_session = "devport"

[service."app/web"]
cwd = "~/src/myapp"
command = ["bin/web", "--port", "${APP_PORT}"]
port = 19000
port_env = "APP_PORT"
env_files = [".env", ".env.local"]
restart = "never"

[service."app/web".health]
type = "http"
url = "/healthz"
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

### Service fields

- `cwd` — working directory (required)
- `command` — command array (required)
- `port` — fixed port number (set exactly one of `port` or `no_port = true`)
- `no_port` — set `true` for services that don't listen on a port
- `port_env` — override the default `PORT` env var name (requires `port`). Use colon-separated names for multiple vars (e.g. `"VITE_PORT:PORT"`)
- `env_files` — list of dotenv files, later files override earlier ones
- `restart` — only `"never"` is supported today
- `health` — health check block (required for every service)
- `public.hostname` — declare a public hostname for ingress export

### Health check types

- `process` — just check if the process is alive
- `http` — HTTP GET with expected status codes (default `[200]`). URL can be a path like `"/"` which resolves to `http://localhost:PORT/` (tries both IPv4 and IPv6)
- `command` — run an arbitrary command, must exit 0
- `none` — no health checking

### Variable interpolation

- `${PORT}` — auto-injected for port-bearing services (unless `port_env` overrides it)
- `${NAME}` — expand from merged environment (process env + env_files + port vars)

### Validation rules

- `version` must be `2`
- each service must set exactly one of `port` or `no_port = true`
- duplicate ports are rejected
- every service needs a `health` block

## Commands

### Start services

```bash
# Start all services from the spec
devport up

# Start only specific services
devport up --key app/web --key jobs/worker

# Start a single service
devport start --key app/web
```

### Stop services

```bash
# Stop all services
devport down

# Stop specific services
devport down --key app/web

# Stop a single service
devport stop --key app/web
```

### Restart a service

```bash
# Restart after code or config changes
devport restart --key app/web
```

Restart is an explicit stop then start. There is no implicit restart on source changes.

### Check status

```bash
# Human-readable table
devport status

# Machine-readable JSON (preferred for agents)
devport status --json

# Show only drift (desired vs actual mismatches)
devport status --diff

# Filter to specific services
devport status --json --key app/web
```

Status reconciles multiple sources (SQLite, tmux, process liveness, file locks) and may repair stale state as a side effect.

### Read logs

```bash
# Recent output from a service's tmux pane (default 200 lines)
devport logs --key app/web

# More lines for debugging
devport logs --key app/web --lines 500
```

### Find a free port

```bash
# Get the next available port from the configured range
devport freeport
```

This is a planning helper — use it when editing the spec to pick a port. It excludes both configured ports and ports already listening on localhost.

### Export ingress rules

```bash
# Export public hostnames as Cloudflare tunnel ingress JSON
devport ingress
```

Output format matches the Cloudflare tunnel API (`PUT /tunnels/<id>/configurations`):

```json
{
  "ingress": [
    { "hostname": "web.example.test", "service": "http://localhost:19000" },
    { "service": "http_status:404" }
  ]
}
```

## Agent Workflow

Typical sequence for managing services:

```bash
# Edit the spec to add/change a service
# (use `devport freeport` to pick ports)

# Apply changes
devport up

# Check for problems
devport status --json --diff

# If a service is unhealthy, read its logs
devport logs --key <service>

# After fixing, restart that service
devport restart --key <service>
```

## Status Model

Services have four states:

- `stopped` — not running
- `starting` — process launched, waiting for health check
- `running` — process alive and healthy
- `failed` — process exited unexpectedly or health check timed out

### Drift

Drift is reported separately from status — a service can be `running` but drifted. Drift reasons include:

- `spec changed since last start` — config hash mismatch, needs restart
- `wrong port listening` — service runs on a different port than configured
- `port not listening` — service claims running but socket is down
- `health check failing` — process alive but health probe fails
- `supervisor lock not held` — supervisor crashed, stale state

## State and Logs

- SQLite database: `~/.local/share/devport/devport.db` (WAL mode)
- Structured log: `~/.local/log/devport.jsonl`
- Lock files: `~/.local/share/devport/locks/`
- Override state dir with `DEVPORT_STATE_DIR`

### SQLite Database

The database is safe to query read-only for diagnostics, automation, or ad-hoc inspection. Use `devport status --json` when possible, but direct SQL is useful for historical data and cross-service queries.

```sql
-- services: current state of each managed service
CREATE TABLE services (
    key TEXT PRIMARY KEY,          -- service key from the TOML spec
    status TEXT NOT NULL,          -- stopped | starting | running | failed
    spec_hash TEXT NOT NULL,       -- hash of the service's config block
    pid INTEGER NOT NULL,          -- child process PID (0 if not running)
    supervisor_pid INTEGER NOT NULL, -- supervisor process PID
    port INTEGER NOT NULL,         -- bound port (0 if no_port)
    no_port INTEGER NOT NULL,      -- 1 if service doesn't use a port
    tmux_window TEXT NOT NULL,     -- tmux window name (deterministic hash)
    restart_count INTEGER NOT NULL,
    last_exit_code INTEGER NOT NULL,
    last_exit_reason TEXT NOT NULL,
    last_error TEXT NOT NULL,
    started_at TEXT NOT NULL,      -- RFC 3339 timestamp
    stopped_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- health_checks: latest health probe result per service
CREATE TABLE health_checks (
    key TEXT PRIMARY KEY,
    check_type TEXT NOT NULL,      -- none | process | http | command
    healthy INTEGER NOT NULL,      -- 1 = healthy, 0 = unhealthy
    detail TEXT NOT NULL,
    checked_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL
);

-- events: append-only log of state transitions
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service_key TEXT NOT NULL,
    level TEXT NOT NULL,           -- info | warn | error
    event TEXT NOT NULL,           -- e.g. service_started, health_check_failed
    data_json TEXT NOT NULL,       -- structured event payload
    created_at TEXT NOT NULL
);
```

The database is at `~/.local/share/devport/devport.db`. Use `python3 -c` for ad-hoc read-only queries:

```bash
# Helper: open the devport DB read-only
#   import sqlite3, os
#   db = sqlite3.connect(f"file:{os.path.expanduser('~/.local/share/devport/devport.db')}?mode=ro", uri=True)
#   db.row_factory = sqlite3.Row

# List all services and their status
python3 -c "
import sqlite3, os
db = sqlite3.connect(f\"file:{os.path.expanduser('~/.local/share/devport/devport.db')}?mode=ro\", uri=True)
db.row_factory = sqlite3.Row
for r in db.execute('SELECT key, status, port, pid FROM services'): print(dict(r))
"

# Which services failed?
python3 -c "
import sqlite3, os
db = sqlite3.connect(f\"file:{os.path.expanduser('~/.local/share/devport/devport.db')}?mode=ro\", uri=True)
db.row_factory = sqlite3.Row
for r in db.execute(\"SELECT key, last_error, stopped_at FROM services WHERE status='failed'\"): print(dict(r))
"

# Recent events for a service
python3 -c "
import sqlite3, os
db = sqlite3.connect(f\"file:{os.path.expanduser('~/.local/share/devport/devport.db')}?mode=ro\", uri=True)
db.row_factory = sqlite3.Row
for r in db.execute('SELECT event, level, data_json, created_at FROM events WHERE service_key=? ORDER BY id DESC LIMIT 20', ('app/web',)): print(dict(r))
"
```

### Structured Log

Query the JSONL log directly:

```bash
# All events
tail -f ~/.local/log/devport.jsonl | jq .

# Errors only
tail -f ~/.local/log/devport.jsonl | jq 'select(.level == "error")'

# One service
tail -f ~/.local/log/devport.jsonl | jq 'select(.service == "app/web")'
```

## Surprising Behavior

- `status` is not purely read-only — it repairs stale DB rows when runtime liveness disagrees
- A `failed` service may still have a zombie child process; recovery paths reap stale PIDs before restarting
- Health check failure is drift, not an automatic transition to `failed`
- `freeport` checks both configured ports and actual listening ports on localhost
- tmux window names are deterministic hashes (not human-readable service names)

## Prerequisites

- Go 1.26+
- `tmux` on PATH
- Unix-like environment (macOS, Linux)
