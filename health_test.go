package devport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeHealthHTTPCommandAndProcess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	defer server.Close()
	go server.Serve(listener)

	port := listener.Addr().(*net.TCPAddr).Port
	httpResult := ProbeHealth(context.Background(), ServiceSpec{
		Port: port,
		Health: HealthSpec{
			Type:         "http",
			URL:          "http://127.0.0.1:${PORT}/",
			ExpectStatus: []int{200, 204},
		},
	}, Environment{values: map[string]string{"PORT": fmt.Sprintf("%d", port)}}, t.TempDir(), func() bool { return true })
	if !httpResult.Healthy {
		t.Fatalf("expected HTTP health to pass: %+v", httpResult)
	}

	dir := t.TempDir()
	commandResult := ProbeHealth(context.Background(), ServiceSpec{
		Health: HealthSpec{
			Type:    "command",
			Command: []string{"sh", "-c", "test -f check.txt"},
		},
	}, Environment{values: map[string]string{}}, dir, func() bool { return true })
	if commandResult.Healthy {
		t.Fatalf("expected command health to fail before file exists")
	}

	if err := os.WriteFile(filepath.Join(dir, "check.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write check.txt: %v", err)
	}
	commandResult = ProbeHealth(context.Background(), ServiceSpec{
		Health: HealthSpec{
			Type:    "command",
			Command: []string{"sh", "-c", "test -f check.txt"},
		},
	}, Environment{values: map[string]string{}}, dir, func() bool { return true })
	if !commandResult.Healthy {
		t.Fatalf("expected command health to pass: %+v", commandResult)
	}

	processResult := ProbeHealth(context.Background(), ServiceSpec{
		Health: HealthSpec{Type: "process"},
	}, Environment{values: map[string]string{}}, dir, func() bool { return true })
	if !processResult.Healthy {
		t.Fatalf("expected process health to pass: %+v", processResult)
	}
}

func TestWaitForStartupRequiresStableProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	callCount := 0
	started := time.Now()
	result, err := WaitForStartup(ctx, ServiceSpec{
		Health: HealthSpec{
			Type:           "process",
			StartupTimeout: Duration(2 * time.Second),
		},
	}, Environment{values: map[string]string{}}, t.TempDir(), func() bool {
		callCount++
		return time.Since(started) < 400*time.Millisecond
	})
	if err == nil {
		t.Fatalf("expected WaitForStartup to fail for unstable process: %+v", result)
	}
	if callCount < 2 {
		t.Fatalf("expected multiple probes, got %d", callCount)
	}
}

func TestEnsurePortAvailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	if err := ensurePortAvailable(port); err == nil {
		t.Fatalf("expected ensurePortAvailable to fail")
	}
}
