package devport_test

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	devport "github.com/hayeah/devportv2"
	_ "modernc.org/sqlite"
)

type dbServiceState struct {
	Key           string
	Status        string
	PID           int
	SupervisorPID int
	Port          int
	TmuxWindow    string
	LastError     string
}

func TestEndToEndChaosMonkey(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required")
	}
	t.Parallel()

	seeds := []int64{11, 29}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			t.Parallel()

			rng := rand.New(rand.NewSource(seed))
			h := newHarness(t)
			start, portA, portB := reserveTCPPortRange(t, 20)
			webPort := portA
			h.writeChaosConfig(start, webPort)
			h.runOK("up", "--file", h.configPath)
			h.assertEventuallyConsistent(webPort)

			for step := 0; step < 16; step++ {
				action := h.runChaosAction(t, rng, &webPort, portA, portB, start)
				t.Logf("seed=%d step=%d action=%s", seed, step, action)
				h.assertEventuallyConsistent(webPort)
			}

			h.runOK("down", "--file", h.configPath)
			h.assertEventuallyConsistent(webPort)
		})
	}
}

func (h *e2eHarness) writeChaosConfig(portRangeStart, webPort int) {
	h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = %d, end = %d }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "chaos-web-%d"]
port = %d
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"

[service."jobs/worker"]
cwd = %q
command = [%q, "sleep", "--duration", "60s", "--message", "chaos-worker"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
startup_timeout = "5s"
`, portRangeStart, portRangeStart+19, h.session, h.root, h.serviceBin, webPort, webPort, h.root, h.serviceBin))
}

func (h *e2eHarness) runChaosAction(t *testing.T, rng *rand.Rand, webPort *int, portA, portB, portRangeStart int) string {
	t.Helper()

	keys := []string{"app/web", "jobs/worker"}
	statuses := h.statusJSON()

	switch rng.Intn(9) {
	case 0:
		h.runOK("up", "--file", h.configPath)
		return "up"
	case 1:
		key := keys[rng.Intn(len(keys))]
		_, _, _ = h.runDetailed("start", "--file", h.configPath, "--key", key)
		return "start:" + key
	case 2:
		key := keys[rng.Intn(len(keys))]
		_, _, _ = h.runDetailed("stop", "--file", h.configPath, "--key", key)
		return "stop:" + key
	case 3:
		key := keys[rng.Intn(len(keys))]
		_, _, _ = h.runDetailed("restart", "--file", h.configPath, "--key", key)
		return "restart:" + key
	case 4:
		if status, ok := randomRunningStatus(rng, statuses); ok && status.PID > 0 {
			_ = syscall.Kill(status.PID, syscall.SIGKILL)
			return fmt.Sprintf("kill-child:%s:%d", status.Key, status.PID)
		}
		return "kill-child:skip"
	case 5:
		if status, ok := randomRunningStatus(rng, statuses); ok && status.SupervisorPID > 0 {
			_ = syscall.Kill(status.SupervisorPID, syscall.SIGKILL)
			return fmt.Sprintf("kill-supervisor:%s:%d", status.Key, status.SupervisorPID)
		}
		return "kill-supervisor:skip"
	case 6:
		if state, ok := h.randomRunningDBState(rng); ok {
			_ = exec.Command("tmux", "kill-window", "-t", h.session+":"+state.TmuxWindow).Run()
			return "kill-window:" + state.Key
		}
		return "kill-window:skip"
	case 7:
		if *webPort == portA {
			*webPort = portB
		} else {
			*webPort = portA
		}
		h.writeChaosConfig(portRangeStart, *webPort)
		web := h.findStatus("app/web")
		if web.Status == "running" {
			drift := h.findStatus("app/web").Drift
			_ = drift
		}
		return fmt.Sprintf("mutate-config:web-port=%d", *webPort)
	default:
		status, ok := randomRunningStatus(rng, statuses)
		if !ok || status.Key != "app/web" {
			return "port-conflict:skip"
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *webPort))
		if err != nil {
			return "port-conflict:skip"
		}
		defer listener.Close()
		_, _, _ = h.runDetailed("restart", "--file", h.configPath, "--key", "app/web")
		return fmt.Sprintf("port-conflict:web-port=%d", *webPort)
	}
}

func randomRunningStatus(rng *rand.Rand, statuses []statusView) (statusView, bool) {
	running := make([]statusView, 0, len(statuses))
	for _, status := range statuses {
		if status.Status == "running" {
			running = append(running, status)
		}
	}
	if len(running) == 0 {
		return statusView{}, false
	}
	return running[rng.Intn(len(running))], true
}

func (h *e2eHarness) randomRunningDBState(rng *rand.Rand) (dbServiceState, bool) {
	states := h.dbStates()
	running := make([]dbServiceState, 0, len(states))
	for _, state := range states {
		if state.Status == "running" {
			running = append(running, state)
		}
	}
	if len(running) == 0 {
		return dbServiceState{}, false
	}
	return running[rng.Intn(len(running))], true
}

func (h *e2eHarness) assertEventuallyConsistent(desiredWebPort int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = h.checkConsistency(desiredWebPort)
		if lastErr == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	h.t.Fatalf("consistency check failed: %v", lastErr)
}

func (h *e2eHarness) checkConsistency(desiredWebPort int) error {
	statuses := h.statusJSON()
	stateByKey := h.dbStates()

	if len(statuses) != 2 {
		return fmt.Errorf("expected 2 statuses, got %d", len(statuses))
	}

	for _, status := range statuses {
		state, ok := stateByKey[status.Key]
		if !ok {
			return fmt.Errorf("missing db state for %s", status.Key)
		}

		lockHeld, err := devport.LockHeld(h.lockPath(state.TmuxWindow))
		if err != nil {
			return fmt.Errorf("lock check for %s: %w", status.Key, err)
		}

		switch status.Status {
		case "starting":
			return fmt.Errorf("service %s remained in starting", status.Key)
		case "running":
			if state.Status != "running" {
				return fmt.Errorf("service %s status mismatch: cli=%s db=%s", status.Key, status.Status, state.Status)
			}
			if state.PID == 0 || state.PID != status.PID {
				return fmt.Errorf("service %s pid mismatch: cli=%d db=%d", status.Key, status.PID, state.PID)
			}
			if !lockHeld {
				return fmt.Errorf("service %s running without lock", status.Key)
			}
			if !h.tmuxWindowExists(state.TmuxWindow) {
				return fmt.Errorf("service %s running without tmux window %s", status.Key, state.TmuxWindow)
			}
			if status.Port > 0 && !portListeningTest(status.Port) {
				return fmt.Errorf("service %s running but port %d is not listening", status.Key, status.Port)
			}
		case "stopped":
			if state.Status != "stopped" {
				return fmt.Errorf("service %s expected stopped db state, got %s", status.Key, state.Status)
			}
			if lockHeld {
				return fmt.Errorf("service %s stopped but lock still held", status.Key)
			}
		case "failed":
			if state.Status != "failed" {
				return fmt.Errorf("service %s expected failed db state, got %s", status.Key, state.Status)
			}
			if lockHeld {
				return fmt.Errorf("service %s failed but lock still held", status.Key)
			}
		default:
			return fmt.Errorf("unknown status %s for %s", status.Status, status.Key)
		}

		if status.Key == "app/web" && status.Status == "running" {
			if desiredWebPort != status.Port {
				if !contains(status.Drift, "spec changed since last start") {
					return fmt.Errorf("web missing spec-change drift: %v", status.Drift)
				}
				if !contains(status.Drift, "wrong port listening") {
					return fmt.Errorf("web missing wrong-port drift: %v", status.Drift)
				}
			}
		}
	}

	return nil
}

func (h *e2eHarness) dbStates() map[string]dbServiceState {
	h.t.Helper()
	db, err := sql.Open("sqlite", h.dbPath())
	if err != nil {
		h.t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(), `
		SELECT key, status, pid, supervisor_pid, port, tmux_window, last_error
		FROM services
	`)
	if err != nil {
		h.t.Fatalf("query services: %v", err)
	}
	defer rows.Close()

	states := map[string]dbServiceState{}
	for rows.Next() {
		var state dbServiceState
		if err := rows.Scan(&state.Key, &state.Status, &state.PID, &state.SupervisorPID, &state.Port, &state.TmuxWindow, &state.LastError); err != nil {
			h.t.Fatalf("scan service state: %v", err)
		}
		states[state.Key] = state
	}
	if err := rows.Err(); err != nil {
		h.t.Fatalf("iterate services: %v", err)
	}
	return states
}

func (h *e2eHarness) tmuxWindowExists(window string) bool {
	output, err := exec.Command("tmux", "list-windows", "-t", h.session, "-F", "#{window_name}").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains("\n"+string(output)+"\n", "\n"+window+"\n")
}

func portListeningTest(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func windowNameForKey(key string) string {
	return devport.NewTmux("test").WindowName(key)
}
