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

func TestPortChecksSupportIPv6Loopback(t *testing.T) {
	listener := listenIPv6Loopback(t)
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	if !portListening(port) {
		t.Fatalf("expected portListening to detect IPv6 loopback listener on port %d", port)
	}
	if err := ensurePortAvailable(port); err == nil {
		t.Fatalf("expected ensurePortAvailable to fail for IPv6 loopback listener on port %d", port)
	}
}

func TestProbeHealthHTTPPathOnly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
		}),
	}
	defer server.Close()
	go server.Serve(listener)

	port := listener.Addr().(*net.TCPAddr).Port
	result := ProbeHealth(context.Background(), ServiceSpec{
		Port: port,
		Health: HealthSpec{
			Type:         "http",
			URL:          "/",
			ExpectStatus: []int{200},
		},
	}, Environment{values: map[string]string{}}, t.TempDir(), func() bool { return true })
	if !result.Healthy {
		t.Fatalf("expected path-only health check to pass: %+v", result)
	}

	resultPath := ProbeHealth(context.Background(), ServiceSpec{
		Port: port,
		Health: HealthSpec{
			Type:         "http",
			URL:          "/healthz",
			ExpectStatus: []int{200},
		},
	}, Environment{values: map[string]string{}}, t.TempDir(), func() bool { return true })
	if !resultPath.Healthy {
		t.Fatalf("expected path-only /healthz health check to pass: %+v", resultPath)
	}
}

func TestProbeHealthHTTPPathOnlyIPv6(t *testing.T) {
	listener := listenIPv6Loopback(t)
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
		}),
	}
	defer server.Close()
	go server.Serve(listener)

	port := listener.Addr().(*net.TCPAddr).Port
	result := ProbeHealth(context.Background(), ServiceSpec{
		Port: port,
		Health: HealthSpec{
			Type:         "http",
			URL:          "/",
			ExpectStatus: []int{200},
		},
	}, Environment{values: map[string]string{}}, t.TempDir(), func() bool { return true })
	if !result.Healthy {
		t.Fatalf("expected path-only health check to pass for IPv6 listener: %+v", result)
	}
}

func TestResolveHealthURL(t *testing.T) {
	env := Environment{values: map[string]string{"APP_PORT": "19005"}}

	if got := resolveHealthURL("/", env, 19005); got != "http://localhost:19005/" {
		t.Fatalf("expected http://localhost:19005/, got %q", got)
	}
	if got := resolveHealthURL("/healthz", env, 8080); got != "http://localhost:8080/healthz" {
		t.Fatalf("expected http://localhost:8080/healthz, got %q", got)
	}
	if got := resolveHealthURL("http://127.0.0.1:${APP_PORT}/", env, 19005); got != "http://127.0.0.1:19005/" {
		t.Fatalf("expected http://127.0.0.1:19005/, got %q", got)
	}
}

func TestProbeHealthHTTPSupportsIPv6Loopback(t *testing.T) {
	listener := listenIPv6Loopback(t)
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	defer server.Close()
	go server.Serve(listener)

	port := listener.Addr().(*net.TCPAddr).Port
	result := ProbeHealth(context.Background(), ServiceSpec{
		Port: port,
		Health: HealthSpec{
			Type:         "http",
			URL:          "http://[::1]:${PORT}/",
			ExpectStatus: []int{204},
		},
	}, Environment{values: map[string]string{"PORT": fmt.Sprintf("%d", port)}}, t.TempDir(), func() bool { return true })
	if !result.Healthy {
		t.Fatalf("expected HTTP health to pass for IPv6 loopback listener: %+v", result)
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

func TestProbeHealthCommandTruncatesDetail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	result := ProbeHealth(ctx, ServiceSpec{
		Health: HealthSpec{
			Type:    "command",
			Command: []string{"sh", "-c", "perl -e 'print \"x\" x 5000; exit 1'"},
		},
	}, Environment{values: map[string]string{}}, dir, func() bool { return true })
	if result.Healthy {
		t.Fatalf("expected health command to fail")
	}
	if len(result.Detail) > maxHealthDetailBytes+len("...(truncated)") {
		t.Fatalf("expected truncated detail, got len=%d", len(result.Detail))
	}
}

func TestProbeHealthHTTPUsesCallerDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			time.Sleep(4 * time.Second)
			writer.WriteHeader(http.StatusOK)
		}),
	}
	defer server.Close()
	go server.Serve(listener)

	port := listener.Addr().(*net.TCPAddr).Port
	service := ServiceSpec{
		Port: port,
		Health: HealthSpec{
			Type:         "http",
			URL:          "http://127.0.0.1:${PORT}/",
			ExpectStatus: []int{200},
		},
	}
	env := Environment{values: map[string]string{"PORT": fmt.Sprintf("%d", port)}}

	backgroundResult := ProbeHealth(context.Background(), service, env, t.TempDir(), func() bool { return true })
	if backgroundResult.Healthy {
		t.Fatalf("expected background probe to hit default timeout")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	deadlineResult := ProbeHealth(ctx, service, env, t.TempDir(), func() bool { return true })
	if !deadlineResult.Healthy {
		t.Fatalf("expected deadline-bound probe to succeed, got %+v", deadlineResult)
	}
}

func listenIPv6Loopback(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	return listener
}
