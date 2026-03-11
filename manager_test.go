package devport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerHelpers(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "devport"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(home, ".config", "devport", "devport.toml")
	configText := `
version = 2
port_range = { start = 19300, end = 19302 }
tmux_session = "devport-unit"

[service."app/web"]
cwd = "/tmp"
command = ["web"]
port = 19300
restart = "never"

[service."app/web".health]
type = "none"

[service."jobs/worker"]
cwd = "/tmp"
command = ["worker"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"

[service."app/web".public]
hostname = "web.example.test"
`
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldHome := os.Getenv("HOME")
	oldState := os.Getenv("DEVPORT_STATE_DIR")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEVPORT_STATE_DIR", oldState)
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("DEVPORT_STATE_DIR", filepath.Join(dir, "state")); err != nil {
		t.Fatalf("set DEVPORT_STATE_DIR: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	manager, err := NewManager("", &stdout, &stderr)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()

	port, err := manager.FreePort(nil)
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	if port != 19301 {
		t.Fatalf("expected free port 19301, got %d", port)
	}

	ingress, err := manager.Ingress(nil)
	if err != nil {
		t.Fatalf("Ingress: %v", err)
	}
	if !strings.Contains(string(ingress), "web.example.test") {
		t.Fatalf("expected ingress output to include hostname: %s", string(ingress))
	}

	statuses, err := manager.Status(context.Background(), nil)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	if err := manager.PrintStatus(statuses, false); err != nil {
		t.Fatalf("PrintStatus: %v", err)
	}
	if !strings.Contains(stdout.String(), "KEY\tSTATUS") && !strings.Contains(stdout.String(), "KEY") {
		t.Fatalf("expected printed status header, got %q", stdout.String())
	}

	if lockPath := manager.lockPath("app/web"); !strings.Contains(lockPath, "svc-") || !strings.HasSuffix(lockPath, ".lock") {
		t.Fatalf("unexpected lock path: %s", lockPath)
	}
}

func containsString(value, fragment string) bool {
	return strings.Contains(value, fragment)
}

func containsStringSlice(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
