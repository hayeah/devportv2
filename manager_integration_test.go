package devport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestManagerLifecycleWithTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required")
	}

	repoRoot, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	tempDir := t.TempDir()
	home := filepath.Join(tempDir, "home")
	binDir := filepath.Join(tempDir, "bin")
	stateDir := filepath.Join(tempDir, "state")
	if err := os.MkdirAll(filepath.Join(home, ".config", "devport"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}

	devportBin := filepath.Join(binDir, "devport")
	serviceBin := filepath.Join(binDir, "testservice")
	buildTestBinary(t, devportBin, "./cli/devport")
	buildTestBinary(t, serviceBin, "./internal/testbin/testservice")

	session := fmt.Sprintf("devport-int-%d", time.Now().UnixNano())
	portRangeStart, webPort, _ := reserveIntegrationTCPPortRange(t, 10)
	configPath := filepath.Join(home, ".config", "devport", "devport.toml")
	configText := fmt.Sprintf(`
version = 2
port_range = { start = %d, end = %d }
tmux_session = %q

[service."app/web"]
cwd = %q
command = [%q, "http", "--port", "${PORT}", "--message", "integration"]
port = %d
restart = "never"

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200]
startup_timeout = "10s"

[service."jobs/worker"]
cwd = %q
command = [%q, "sleep", "--duration", "60s", "--message", "worker"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
startup_timeout = "5s"
`, portRangeStart, portRangeStart+9, session, repoRoot, serviceBin, webPort, repoRoot, serviceBin)
	if err := os.WriteFile(configPath, []byte(strings.TrimSpace(configText)+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldHome := os.Getenv("HOME")
	oldState := os.Getenv("DEVPORT_STATE_DIR")
	oldPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEVPORT_STATE_DIR", oldState)
		_ = os.Setenv("PATH", oldPath)
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("DEVPORT_STATE_DIR", stateDir); err != nil {
		t.Fatalf("set DEVPORT_STATE_DIR: %v", err)
	}
	if err := os.Setenv("PATH", binDir+":"+oldPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	manager, err := NewManager(configPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	manager.executable = devportBin

	ctx := context.Background()
	if err := manager.Up(ctx, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}

	statuses, err := manager.Status(ctx, nil)
	if err != nil {
		t.Fatalf("Status after Up: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	webStatus := findStatusByKey(t, statuses, "app/web")
	if webStatus.Status != "running" || webStatus.Health != "healthy" {
		t.Fatalf("unexpected web status after Up: %+v", webStatus)
	}
	workerStatus := findStatusByKey(t, statuses, "jobs/worker")
	if workerStatus.Status != "running" || workerStatus.Health != "healthy" {
		t.Fatalf("unexpected worker status after Up: %+v", workerStatus)
	}

	logs, err := manager.Logs(ctx, "app/web", 100)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !strings.Contains(logs, "http_service_starting") {
		t.Fatalf("expected app/web logs, got %q", logs)
	}

	beforeRestart := findServiceRecord(t, manager, "app/web")
	if err := manager.Restart(ctx, "app/web"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	afterRestart := findServiceRecord(t, manager, "app/web")
	if afterRestart.PID == beforeRestart.PID {
		t.Fatalf("expected PID to change after restart")
	}
	if afterRestart.RestartCount != beforeRestart.RestartCount+1 {
		t.Fatalf("expected restart count to increment: before=%d after=%d", beforeRestart.RestartCount, afterRestart.RestartCount)
	}

	if err := manager.Stop(ctx, "app/web", "test-stop"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	record := findServiceRecord(t, manager, "app/web")
	if record.Status != "stopped" {
		t.Fatalf("expected stopped service record, got %+v", record)
	}

	if err := manager.Start(ctx, "app/web", "test-start"); err != nil {
		t.Fatalf("Start after stop: %v", err)
	}

	record = findServiceRecord(t, manager, "app/web")
	if err := syscall.Kill(record.SupervisorPID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill supervisor: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statuses, err = manager.Status(ctx, []string{"app/web"})
		if err != nil {
			t.Fatalf("Status after supervisor kill: %v", err)
		}
		if findStatusByKey(t, statuses, "app/web").Status == "failed" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	reconciled := findStatusByKey(t, statuses, "app/web")
	if reconciled.Status != "failed" {
		t.Fatalf("expected failed status after supervisor kill, got %+v", reconciled)
	}
	if err := manager.Up(ctx, []string{"app/web"}); err != nil {
		t.Fatalf("Up after supervisor kill: %v", err)
	}
	restarted := findServiceRecord(t, manager, "app/web")
	if restarted.Status != "running" || restarted.PID == 0 {
		t.Fatalf("expected running service after recovery, got %+v", restarted)
	}

	if err := manager.Stop(ctx, "app/web", "prepare-concurrent-start"); err != nil {
		t.Fatalf("Stop before concurrent start: %v", err)
	}

	results := make(chan error, 2)
	var startWG sync.WaitGroup
	startWG.Add(2)
	for range 2 {
		go func() {
			defer startWG.Done()
			results <- manager.Start(ctx, "app/web", "concurrent-start")
		}()
	}
	startWG.Wait()
	close(results)

	var successCount int
	var busyCount int
	for err := range results {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, context.Canceled):
			t.Fatalf("unexpected context cancellation: %v", err)
		case strings.Contains(err.Error(), `service "app/web" is busy`):
			busyCount++
		default:
			t.Fatalf("unexpected concurrent start error: %v", err)
		}
	}
	if successCount != 1 || busyCount != 1 {
		t.Fatalf("expected one success and one busy error, got success=%d busy=%d", successCount, busyCount)
	}

	if err := manager.Down(ctx, nil); err != nil {
		t.Fatalf("Down: %v", err)
	}

	if err := manager.CleanupSession(); err != nil {
		t.Fatalf("CleanupSession: %v", err)
	}
}

func buildTestBinary(t *testing.T, output, target string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, target)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", target, err, string(out))
	}
}

func reserveIntegrationTCPPortRange(t *testing.T, extra int) (int, int, int) {
	t.Helper()

	listeners := make([]net.Listener, 0, 2)
	ports := make([]int, 0, 2)
	for len(ports) < 2 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve tcp port: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		duplicate := false
		for _, existing := range ports {
			if existing == port {
				duplicate = true
				break
			}
		}
		if duplicate {
			_ = listener.Close()
			continue
		}
		listeners = append(listeners, listener)
		ports = append(ports, port)
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}

	start := min(ports[0], ports[1])
	end := max(ports[0], ports[1]) + extra
	if end > 65535 {
		start -= end - 65535
		if start < 1024 {
			t.Fatalf("reserved port range overflow: start=%d end=%d", start, end)
		}
	}
	return start, ports[0], ports[1]
}

func findStatusByKey(t *testing.T, statuses []StatusView, key string) StatusView {
	t.Helper()
	for _, status := range statuses {
		if status.Key == key {
			return status
		}
	}
	t.Fatalf("status %s not found", key)
	return StatusView{}
}

func findServiceRecord(t *testing.T, manager *Manager, key string) *ServiceRecord {
	t.Helper()
	record, err := manager.store.Service(context.Background(), key)
	if err != nil {
		t.Fatalf("store.Service(%s): %v", key, err)
	}
	if record == nil {
		t.Fatalf("service record %s not found", key)
	}
	return record
}
