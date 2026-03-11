package devport_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type e2eHarness struct {
	t          *testing.T
	root       string
	home       string
	binDir     string
	devportBin string
	serviceBin string
	configPath string
	session    string
}

type statusView struct {
	Key           string   `json:"key"`
	Status        string   `json:"status"`
	Health        string   `json:"health"`
	PID           int      `json:"pid"`
	SupervisorPID int      `json:"supervisor_pid"`
	RestartCount  int      `json:"restart_count"`
	Port          int      `json:"port"`
	Drift         []string `json:"drift"`
	LastError     string   `json:"last_error"`
}

func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required")
	}

	t.Run("up_status_logs_ingress_down", func(t *testing.T) {
		h := newHarness(t)
		h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19170, end = 19179 }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "hello-web"]
port = 19173
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"

[service."app/web".public]
hostname = "web.example.test"

[service."jobs/worker"]
cwd = %q
command = [%q, "sleep", "--duration", "60s", "--message", "worker-loop"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
startup_timeout = "5s"
`, h.session, h.root, h.serviceBin, h.root, h.serviceBin))

		h.runOK("up", "--file", h.configPath)

		statuses := h.statusJSON()
		if len(statuses) != 2 {
			t.Fatalf("expected 2 statuses, got %d", len(statuses))
		}
		for _, status := range statuses {
			if status.Status != "running" {
				t.Fatalf("expected %s to be running, got %s", status.Key, status.Status)
			}
			if status.Health != "healthy" {
				t.Fatalf("expected %s to be healthy, got %s", status.Key, status.Health)
			}
			if len(status.Drift) != 0 {
				t.Fatalf("expected no drift for %s, got %v", status.Key, status.Drift)
			}
		}

		logs := h.runOK("logs", "--file", h.configPath, "--key", "app/web")
		if !strings.Contains(logs, "http_service_starting") {
			t.Fatalf("expected web logs to contain startup output, got: %s", logs)
		}

		ingress := h.runOK("ingress", "--file", h.configPath)
		if !strings.Contains(ingress, `"hostname": "web.example.test"`) {
			t.Fatalf("expected ingress hostname, got: %s", ingress)
		}
		if !strings.Contains(ingress, `"service": "http_status:404"`) {
			t.Fatalf("expected ingress catch-all rule, got: %s", ingress)
		}

		h.runOK("down", "--file", h.configPath)
		statuses = h.statusJSON()
		for _, status := range statuses {
			if status.Status != "stopped" {
				t.Fatalf("expected %s to be stopped, got %s", status.Key, status.Status)
			}
		}
	})

	t.Run("restart_changes_pid_and_counter", func(t *testing.T) {
		h := newHarness(t)
		h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19180, end = 19189 }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "restart-me"]
port = 19180
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"
`, h.session, h.root, h.serviceBin))

		h.runOK("start", "--file", h.configPath, "--key", "app/web")
		first := h.findStatus("app/web")
		if first.PID == 0 {
			t.Fatalf("expected PID to be recorded")
		}
		if first.RestartCount != 0 {
			t.Fatalf("expected restart_count=0, got %d", first.RestartCount)
		}

		h.runOK("restart", "--file", h.configPath, "--key", "app/web")
		second := h.findStatus("app/web")
		if second.PID == 0 || second.PID == first.PID {
			t.Fatalf("expected PID to change after restart: before=%d after=%d", first.PID, second.PID)
		}
		if second.RestartCount != 1 {
			t.Fatalf("expected restart_count=1, got %d", second.RestartCount)
		}
	})

	t.Run("failed_start_records_failed_status", func(t *testing.T) {
		h := newHarness(t)
		h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19190, end = 19199 }
tmux_session = %q

[service."app/fail"]
cwd = %q
command = [%q, "fail", "--code", "9", "--message", "boom"]
no_port = true
restart = "never"

[service."app/fail".health]
type = "process"
startup_timeout = "3s"
`, h.session, h.root, h.serviceBin))

		output, err := h.run("start", "--file", h.configPath, "--key", "app/fail")
		if err == nil {
			t.Fatalf("expected failing service start to exit non-zero")
		}
		if !strings.Contains(output, "startup timeout") && !strings.Contains(output, "failed") {
			t.Fatalf("expected failing output, got: %s", output)
		}

		status := h.findStatus("app/fail")
		if status.Status != "failed" {
			t.Fatalf("expected failed status, got %s", status.Status)
		}
		if status.LastError == "" {
			t.Fatalf("expected last_error to be recorded")
		}

		logs := h.runOK("logs", "--file", h.configPath, "--key", "app/fail")
		if !strings.Contains(logs, "fail_service_exiting") {
			t.Fatalf("expected failure logs, got: %s", logs)
		}
	})

	t.Run("status_detects_spec_drift", func(t *testing.T) {
		h := newHarness(t)
		h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19200, end = 19209 }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "drift-v1"]
port = 19200
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"
`, h.session, h.root, h.serviceBin))

		h.runOK("start", "--file", h.configPath, "--key", "app/web")

		h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19200, end = 19209 }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "drift-v2"]
port = 19201
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"
`, h.session, h.root, h.serviceBin))

		status := h.findStatus("app/web")
		if !contains(status.Drift, "spec changed since last start") {
			t.Fatalf("expected spec drift, got %v", status.Drift)
		}
		if !contains(status.Drift, "wrong port listening") {
			t.Fatalf("expected wrong-port drift, got %v", status.Drift)
		}
	})

	t.Run("freeport_skips_spec_ports_and_live_listener", func(t *testing.T) {
		h := newHarness(t)
		listener, err := net.Listen("tcp", "127.0.0.1:19211")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer listener.Close()

		h.writeConfig(fmt.Sprintf(`
version = 2
port_range = { start = 19210, end = 19212 }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "port-check"]
port = 19210
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"
`, h.session, h.root, h.serviceBin))

		output := strings.TrimSpace(h.runOK("freeport", "--file", h.configPath))
		if output != "19212" {
			t.Fatalf("expected free port 19212, got %s", output)
		}
	})
}

func newHarness(t *testing.T) *e2eHarness {
	t.Helper()

	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	tempDir := t.TempDir()
	home := filepath.Join(tempDir, "home")
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	devportBin := filepath.Join(binDir, "devport")
	serviceBin := filepath.Join(binDir, "testservice")
	buildBinary(t, devportBin, "./cli/devport")
	buildBinary(t, serviceBin, "./internal/testbin/testservice")

	h := &e2eHarness{
		t:          t,
		root:       root,
		home:       home,
		binDir:     binDir,
		devportBin: devportBin,
		serviceBin: serviceBin,
		configPath: filepath.Join(tempDir, "devport.toml"),
		session:    fmt.Sprintf("devport-e2e-%d", time.Now().UnixNano()),
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", h.session).Run()
	})
	return h
}

func buildBinary(t *testing.T, output, target string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, target)
	command.Dir = "."
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", target, err, string(out))
	}
}

func (h *e2eHarness) writeConfig(contents string) {
	h.t.Helper()
	if err := os.WriteFile(h.configPath, []byte(strings.TrimSpace(contents)+"\n"), 0o644); err != nil {
		h.t.Fatalf("write config: %v", err)
	}
}

func (h *e2eHarness) env() []string {
	return append(os.Environ(),
		"HOME="+h.home,
		"PATH="+h.binDir+":"+os.Getenv("PATH"),
	)
}

func (h *e2eHarness) runOK(args ...string) string {
	h.t.Helper()
	stdout, stderr, err := h.runDetailed(args...)
	if err != nil {
		h.t.Fatalf("run %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
	}
	return stdout
}

func (h *e2eHarness) run(args ...string) (string, error) {
	h.t.Helper()
	stdout, stderr, err := h.runDetailed(args...)
	return stdout + stderr, err
}

func (h *e2eHarness) runDetailed(args ...string) (string, string, error) {
	h.t.Helper()
	command := exec.Command(h.devportBin, args...)
	command.Env = h.env()
	command.Dir = h.root
	var stdout strings.Builder
	var stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func (h *e2eHarness) statusJSON() []statusView {
	h.t.Helper()
	output := h.runOK("status", "--file", h.configPath, "--json")
	var statuses []statusView
	if err := json.Unmarshal([]byte(output), &statuses); err != nil {
		h.t.Fatalf("decode status json: %v\n%s", err, output)
	}
	return statuses
}

func (h *e2eHarness) findStatus(key string) statusView {
	h.t.Helper()
	for _, status := range h.statusJSON() {
		if status.Key == key {
			return status
		}
	}
	h.t.Fatalf("status %s not found", key)
	return statusView{}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
