package devport_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"syscall"
	"testing"
	"time"

	devport "github.com/hayeah/devportv2"
	_ "modernc.org/sqlite"
)

type invariantStatusView struct {
	Key           string   `json:"key"`
	Status        string   `json:"status"`
	Health        string   `json:"health"`
	PID           int      `json:"pid"`
	SupervisorPID int      `json:"supervisor_pid"`
	Drift         []string `json:"drift"`
	LastError     string   `json:"last_error"`
	LastReason    string   `json:"last_reason"`
}

type invariantDBState struct {
	Status        string
	PID           int
	SupervisorPID int
	TmuxWindow    string
	LastError     string
	LastReason    string
}

type invariantHealthState struct {
	Healthy bool
	Detail  string
}

func TestEndToEndInvariantChaosRecovery(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required")
	}
	t.Parallel()

	t.Run("status_reconciles_stale_starting_without_lock", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.writeWorkerOnlyConfig()
		t.Cleanup(func() {
			_, _, _ = h.runDetailed("down", "--file", h.configPath)
		})

		h.runOK("start", "--file", h.configPath, "--key", "jobs/worker")
		original := h.findInvariantStatus("jobs/worker")
		if original.PID == 0 || original.SupervisorPID == 0 {
			t.Fatalf("expected running worker with recorded pids: %+v", original)
		}

		if err := syscall.Kill(original.SupervisorPID, syscall.SIGKILL); err != nil {
			t.Fatalf("kill supervisor: %v", err)
		}
		h.waitForLockState("jobs/worker", false)
		h.waitForProcessState(original.PID, true)

		h.updateServiceFailureFields("jobs/worker", "starting", "", "")

		status := h.findInvariantStatus("jobs/worker")
		if status.Status != "failed" {
			t.Fatalf("expected status failed after stale starting reconcile, got %+v", status)
		}
		if status.Health != "unhealthy" {
			t.Fatalf("expected unhealthy health after stale starting reconcile, got %+v", status)
		}
		if !contains(status.Drift, "supervisor lock not held") {
			t.Fatalf("expected supervisor lock drift, got %+v", status.Drift)
		}
		if status.LastReason != "supervisor_missing" {
			t.Fatalf("expected last_reason=supervisor_missing, got %+v", status)
		}
		if status.LastError != "supervisor lock not held" {
			t.Fatalf("expected last_error to be backfilled, got %+v", status)
		}

		state := h.dbInvariantState("jobs/worker")
		if state.Status != "failed" {
			t.Fatalf("expected db failed state, got %+v", state)
		}
		if state.SupervisorPID != 0 {
			t.Fatalf("expected reconciled db supervisor pid to be cleared, got %+v", state)
		}
		if state.LastReason != "supervisor_missing" {
			t.Fatalf("expected db last_reason=supervisor_missing, got %+v", state)
		}
		if state.LastError != "supervisor lock not held" {
			t.Fatalf("expected db last_error to be backfilled, got %+v", state)
		}

		health := h.dbHealthState("jobs/worker")
		if health.Healthy {
			t.Fatalf("expected unhealthy persisted health after reconcile, got %+v", health)
		}
		if health.Detail != "supervisor lock not held" {
			t.Fatalf("expected persisted health detail, got %+v", health)
		}
	})

	t.Run("start_reaps_orphan_child_after_supervisor_kill", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.writeWorkerOnlyConfig()
		t.Cleanup(func() {
			_, _, _ = h.runDetailed("down", "--file", h.configPath)
		})

		h.runOK("start", "--file", h.configPath, "--key", "jobs/worker")
		original := h.findInvariantStatus("jobs/worker")
		if original.PID == 0 || original.SupervisorPID == 0 {
			t.Fatalf("expected running worker with recorded pids: %+v", original)
		}

		if err := syscall.Kill(original.SupervisorPID, syscall.SIGKILL); err != nil {
			t.Fatalf("kill supervisor: %v", err)
		}
		h.waitForLockState("jobs/worker", false)
		h.waitForProcessState(original.PID, true)

		h.runOK("start", "--file", h.configPath, "--key", "jobs/worker")
		h.waitForLockState("jobs/worker", true)
		h.waitForProcessState(original.PID, false)

		status := h.findInvariantStatus("jobs/worker")
		if status.Status != "running" {
			t.Fatalf("expected running status after recovery start, got %+v", status)
		}
		if status.PID == 0 || status.PID == original.PID {
			t.Fatalf("expected start to replace orphan pid=%d, got %+v", original.PID, status)
		}
		if status.Health != "healthy" {
			t.Fatalf("expected healthy status after recovery start, got %+v", status)
		}
		if len(status.Drift) != 0 {
			t.Fatalf("expected no drift after recovery start, got %+v", status.Drift)
		}

		state := h.dbInvariantState("jobs/worker")
		if state.Status != "running" {
			t.Fatalf("expected db running state after recovery start, got %+v", state)
		}
		if state.PID != status.PID {
			t.Fatalf("expected db pid=%d to match status, got %+v", status.PID, state)
		}
		if !h.tmuxWindowExists(windowNameForKey("jobs/worker")) {
			t.Fatalf("expected tmux window to exist after recovery start")
		}
	})

	t.Run("stop_cleans_orphan_child_after_supervisor_kill", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.writeWorkerOnlyConfig()
		t.Cleanup(func() {
			_, _, _ = h.runDetailed("down", "--file", h.configPath)
		})

		h.runOK("start", "--file", h.configPath, "--key", "jobs/worker")
		original := h.findInvariantStatus("jobs/worker")
		if original.PID == 0 || original.SupervisorPID == 0 {
			t.Fatalf("expected running worker with recorded pids: %+v", original)
		}

		if err := syscall.Kill(original.SupervisorPID, syscall.SIGKILL); err != nil {
			t.Fatalf("kill supervisor: %v", err)
		}
		h.waitForLockState("jobs/worker", false)
		h.waitForProcessState(original.PID, true)

		reconciled := h.findInvariantStatus("jobs/worker")
		if reconciled.Status != "failed" {
			t.Fatalf("expected status reconcile to mark worker failed, got %+v", reconciled)
		}

		h.runOK("stop", "--file", h.configPath, "--key", "jobs/worker")
		h.waitForProcessState(original.PID, false)
		h.waitForLockState("jobs/worker", false)

		status := h.findInvariantStatus("jobs/worker")
		if status.Status != "stopped" {
			t.Fatalf("expected stopped status after recovery stop, got %+v", status)
		}
		if status.PID != 0 || status.SupervisorPID != 0 {
			t.Fatalf("expected zero pids after recovery stop, got %+v", status)
		}
		if status.LastReason != "stop" {
			t.Fatalf("expected last_reason=stop after recovery stop, got %+v", status)
		}

		state := h.dbInvariantState("jobs/worker")
		if state.Status != "stopped" {
			t.Fatalf("expected db stopped state after recovery stop, got %+v", state)
		}
		if state.PID != 0 || state.SupervisorPID != 0 {
			t.Fatalf("expected zero db pids after recovery stop, got %+v", state)
		}
		if state.LastReason != "stop" {
			t.Fatalf("expected db last_reason=stop after recovery stop, got %+v", state)
		}
		if h.tmuxWindowExists(windowNameForKey("jobs/worker")) {
			t.Fatalf("expected tmux window to be removed after recovery stop")
		}
	})
}

func (h *e2eHarness) writeWorkerOnlyConfig() {
	h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19300, end = 19309 }
tmux_session = %q

[service."jobs/worker"]
cwd = %q
command = [%q, "sleep", "--duration", "60s", "--message", "worker-chaos"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
startup_timeout = "5s"
`, h.session, h.root, h.serviceBin))
}

func (h *e2eHarness) invariantStatuses() []invariantStatusView {
	h.t.Helper()

	output := h.runOK("status", "--file", h.configPath, "--json")
	var statuses []invariantStatusView
	if err := json.Unmarshal([]byte(output), &statuses); err != nil {
		h.t.Fatalf("decode invariant status json: %v\n%s", err, output)
	}
	return statuses
}

func (h *e2eHarness) findInvariantStatus(key string) invariantStatusView {
	h.t.Helper()

	for _, status := range h.invariantStatuses() {
		if status.Key == key {
			return status
		}
	}
	h.t.Fatalf("status %s not found", key)
	return invariantStatusView{}
}

func (h *e2eHarness) updateServiceFailureFields(key, status, lastReason, lastError string) {
	h.t.Helper()

	db, err := sql.Open("sqlite", h.dbPath())
	if err != nil {
		h.t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	result, err := db.ExecContext(context.Background(), `
		UPDATE services
		SET status = ?, last_exit_reason = ?, last_error = ?
		WHERE key = ?
	`, status, lastReason, lastError, key)
	if err != nil {
		h.t.Fatalf("update services: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		h.t.Fatalf("service rows affected: %v", err)
	}
	if rowsAffected != 1 {
		h.t.Fatalf("expected one service row to update, got %d", rowsAffected)
	}
}

func (h *e2eHarness) dbInvariantState(key string) invariantDBState {
	h.t.Helper()

	db, err := sql.Open("sqlite", h.dbPath())
	if err != nil {
		h.t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	row := db.QueryRowContext(context.Background(), `
		SELECT status, pid, supervisor_pid, tmux_window, last_error, last_exit_reason
		FROM services
		WHERE key = ?
	`, key)
	var state invariantDBState
	if err := row.Scan(&state.Status, &state.PID, &state.SupervisorPID, &state.TmuxWindow, &state.LastError, &state.LastReason); err != nil {
		h.t.Fatalf("scan service state: %v", err)
	}
	return state
}

func (h *e2eHarness) dbHealthState(key string) invariantHealthState {
	h.t.Helper()

	db, err := sql.Open("sqlite", h.dbPath())
	if err != nil {
		h.t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	row := db.QueryRowContext(context.Background(), `
		SELECT healthy, detail
		FROM health_checks
		WHERE key = ?
	`, key)
	var healthy int
	var state invariantHealthState
	if err := row.Scan(&healthy, &state.Detail); err != nil {
		h.t.Fatalf("scan health state: %v", err)
	}
	state.Healthy = healthy == 1
	return state
}

func (h *e2eHarness) waitForLockState(key string, wantHeld bool) {
	h.t.Helper()

	lockPath := h.lockPath(windowNameForKey(key))
	h.waitForCondition(fmt.Sprintf("lock held=%t for %s", wantHeld, key), func() bool {
		held, err := devport.LockHeld(lockPath)
		return err == nil && held == wantHeld
	})
}

func (h *e2eHarness) waitForProcessState(pid int, wantAlive bool) {
	h.t.Helper()

	h.waitForCondition(fmt.Sprintf("pid %d alive=%t", pid, wantAlive), func() bool {
		return processAliveTest(pid) == wantAlive
	})
}

func (h *e2eHarness) waitForCondition(description string, check func() bool) {
	h.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", description)
}

func processAliveTest(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
