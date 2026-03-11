package devport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
)

type HealthResult struct {
	Healthy  bool
	Detail   string
	Duration time.Duration
}

const processStabilizationWindow = 750 * time.Millisecond
const defaultHTTPHealthTimeout = 3 * time.Second
const maxHealthDetailBytes = 2048

func WaitForStartup(ctx context.Context, service ServiceSpec, env Environment, cwd string, processAlive func() bool) (HealthResult, error) {
	timeout := service.Health.StartupTimeout.Duration()
	deadline := time.Now().Add(timeout)
	var healthySince time.Time

	for {
		result := ProbeHealth(ctx, service, env, cwd, processAlive)
		if result.Healthy {
			if service.Health.Type == "process" {
				if healthySince.IsZero() {
					healthySince = time.Now()
				}
				if time.Since(healthySince) < processStabilizationWindow {
					goto wait
				}
			}
			return result, nil
		}
		healthySince = time.Time{}
		if time.Now().After(deadline) {
			return result, fmt.Errorf("startup timeout: %s", result.Detail)
		}
	wait:
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func ProbeHealth(ctx context.Context, service ServiceSpec, env Environment, cwd string, processAlive func() bool) HealthResult {
	started := time.Now()

	switch service.Health.Type {
	case "none":
		return HealthResult{Healthy: true, Detail: "disabled", Duration: time.Since(started)}
	case "process":
		if processAlive() {
			return HealthResult{Healthy: true, Detail: "process alive", Duration: time.Since(started)}
		}
		return HealthResult{Healthy: false, Detail: "process not alive", Duration: time.Since(started)}
	case "http":
		if service.Port > 0 && !portListening(service.Port) {
			return HealthResult{Healthy: false, Detail: "port not listening", Duration: time.Since(started)}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, env.ExpandString(service.Health.URL), nil)
		if err != nil {
			return HealthResult{Healthy: false, Detail: truncateHealthDetail(err.Error()), Duration: time.Since(started)}
		}
		client := &http.Client{Timeout: defaultHTTPHealthTimeout}
		response, err := client.Do(request)
		if err != nil {
			return HealthResult{Healthy: false, Detail: truncateHealthDetail(err.Error()), Duration: time.Since(started)}
		}
		defer response.Body.Close()
		if slices.Contains(service.Health.ExpectStatus, response.StatusCode) {
			return HealthResult{Healthy: true, Detail: fmt.Sprintf("http %d", response.StatusCode), Duration: time.Since(started)}
		}
		return HealthResult{Healthy: false, Detail: fmt.Sprintf("unexpected status %d", response.StatusCode), Duration: time.Since(started)}
	case "command":
		command := env.ExpandSlice(service.Health.Command)
		if len(command) == 0 {
			return HealthResult{Healthy: false, Detail: "health command is empty", Duration: time.Since(started)}
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = cwd
		cmd.Env = env.Environ()
		output, err := cmd.CombinedOutput()
		if err != nil {
			return HealthResult{
				Healthy:  false,
				Detail:   truncateHealthDetail(fmt.Sprintf("%v: %s", err, string(output))),
				Duration: time.Since(started),
			}
		}
		return HealthResult{Healthy: true, Detail: "command succeeded", Duration: time.Since(started)}
	default:
		return HealthResult{Healthy: false, Detail: fmt.Sprintf("unsupported health type %s", service.Health.Type), Duration: time.Since(started)}
	}
}

func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func ensurePortAvailable(port int) error {
	if port == 0 {
		return nil
	}
	if portListening(port) {
		return fmt.Errorf("port %d is already in use", port)
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func truncateHealthDetail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxHealthDetailBytes {
		return value
	}
	return value[:maxHealthDetailBytes] + "...(truncated)"
}
