package devport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/hayeah/devportv2/logger"
)

type Manager struct {
	paths      Paths
	config     *Config
	store      *Store
	tmux       *Tmux
	log        *slog.Logger
	stdout     io.Writer
	stderr     io.Writer
	executable string
}

type StatusView struct {
	Key           string   `json:"key"`
	Status        string   `json:"status"`
	Health        string   `json:"health"`
	PID           int      `json:"pid"`
	SupervisorPID int      `json:"supervisor_pid"`
	Port          int      `json:"port"`
	NoPort        bool     `json:"no_port"`
	RestartCount  int      `json:"restart_count"`
	PublicHost    string   `json:"public_hostname,omitempty"`
	Drift         []string `json:"drift"`
	LastError     string   `json:"last_error,omitempty"`
	LastExitCode  int      `json:"last_exit_code,omitempty"`
	LastReason    string   `json:"last_reason,omitempty"`
}

type IngressDocument struct {
	Ingress []IngressRule `json:"ingress"`
}

func NewManager(explicitConfig string, stdout, stderr io.Writer) (*Manager, error) {
	paths, err := ResolvePaths(explicitConfig)
	if err != nil {
		return nil, err
	}

	config, err := LoadConfig(paths.Config)
	if err != nil {
		return nil, err
	}

	store, err := OpenStore(paths.DB)
	if err != nil {
		return nil, err
	}

	executable, err := os.Executable()
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	manager := &Manager{
		paths:      paths,
		config:     config,
		store:      store,
		tmux:       NewTmux(config.TmuxSession),
		log:        logger.New("devport"),
		stdout:     stdout,
		stderr:     stderr,
		executable: executable,
	}

	manager.log.Info("spec_loaded", "config", paths.Config, "services", len(config.Services))
	return manager, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	return m.store.Close()
}

func (m *Manager) Start(ctx context.Context, key, cause string) error {
	service, err := m.config.Service(key)
	if err != nil {
		return err
	}
	if err := ensurePortAvailable(service.Port); err != nil {
		return err
	}

	lockHeld, err := LockHeld(m.lockPath(key))
	if err != nil {
		return err
	}
	if lockHeld {
		return fmt.Errorf("service %q is already running", key)
	}

	window := m.tmux.WindowName(key)
	command := []string{m.executable, "supervise", "--file", m.paths.Config, "--key", key}
	if err := m.tmux.Start(window, command); err != nil {
		return err
	}

	deadline := time.Now().Add(service.Health.StartupTimeout.Duration() + 2*time.Second)
	for time.Now().Before(deadline) {
		record, err := m.store.Service(ctx, key)
		if err != nil {
			return err
		}
		if record != nil {
			switch record.Status {
			case "running":
				return nil
			case "failed":
				if record.LastError != "" {
					return errors.New(record.LastError)
				}
				return fmt.Errorf("service %q failed to start", key)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for service %q to report ready", key)
}

func (m *Manager) Stop(ctx context.Context, key, reason string) error {
	record, err := m.store.Service(ctx, key)
	if err != nil {
		return err
	}
	window := m.tmux.WindowName(key)
	if record == nil {
		_ = m.tmux.KillWindow(window)
		return nil
	}

	lockHeld, err := LockHeld(m.lockPath(key))
	if err != nil {
		return err
	}

	if lockHeld && record.SupervisorPID > 0 {
		if err := syscall.Kill(record.SupervisorPID, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			lockHeld, err = LockHeld(m.lockPath(key))
			if err != nil {
				return err
			}
			if !lockHeld {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	if err := m.tmux.KillWindow(window); err != nil {
		return err
	}

	record.Status = "stopped"
	record.LastExitReason = reason
	record.StoppedAt = nowUTC()
	record.LastError = ""
	record.PID = 0
	record.SupervisorPID = 0
	if err := m.store.UpsertService(ctx, *record); err != nil {
		return err
	}
	return m.store.RecordEvent(ctx, key, "info", "service_stopped", map[string]any{"reason": reason})
}

func (m *Manager) Restart(ctx context.Context, key string) error {
	if err := m.Stop(ctx, key, "restart"); err != nil {
		return err
	}

	record, err := m.store.Service(ctx, key)
	if err != nil {
		return err
	}
	if record != nil {
		record.RestartCount++
		if err := m.store.UpsertService(ctx, *record); err != nil {
			return err
		}
	}
	return m.Start(ctx, key, "restart")
}

func (m *Manager) Up(ctx context.Context, keys []string) error {
	selected, err := m.config.ServiceKeys(keys)
	if err != nil {
		return err
	}

	failures := []string{}
	for _, key := range selected {
		if err := m.Start(ctx, key, "up"); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func (m *Manager) Down(ctx context.Context, keys []string) error {
	selected, err := m.config.ServiceKeys(keys)
	if err != nil {
		return err
	}
	for _, key := range selected {
		if err := m.Stop(ctx, key, "down"); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Logs(ctx context.Context, key string, lines int) (string, error) {
	record, err := m.store.Service(ctx, key)
	if err != nil {
		return "", err
	}
	window := m.tmux.WindowName(key)
	if record != nil && record.TmuxWindow != "" {
		window = record.TmuxWindow
	}
	return m.tmux.CapturePane(window, lines)
}

func (m *Manager) FreePort(keys []string) (int, error) {
	if m.config.PortRange.Start == 0 || m.config.PortRange.End == 0 {
		return 0, fmt.Errorf("config must define port_range")
	}

	used := map[int]bool{}
	selected, err := m.config.ServiceKeys(keys)
	if err != nil && len(keys) > 0 {
		return 0, err
	}
	if len(keys) == 0 {
		selected, _ = m.config.ServiceKeys(nil)
	}
	for _, key := range selected {
		service := m.config.Services[key]
		if service.Port > 0 {
			used[service.Port] = true
		}
	}

	for port := m.config.PortRange.Start; port <= m.config.PortRange.End; port++ {
		if used[port] {
			continue
		}
		if !portListening(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in range %d-%d", m.config.PortRange.Start, m.config.PortRange.End)
}

func (m *Manager) Ingress(keys []string) ([]byte, error) {
	rules, err := m.config.IngressRules(keys)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(IngressDocument{Ingress: rules}, "", "  ")
}

func (m *Manager) Status(ctx context.Context, keys []string) ([]StatusView, error) {
	selected, err := m.config.ServiceKeys(keys)
	if err != nil {
		return nil, err
	}

	statuses := make([]StatusView, 0, len(selected))
	for _, key := range selected {
		service := m.config.Services[key]
		record, err := m.store.Service(ctx, key)
		if err != nil {
			return nil, err
		}

		view := StatusView{
			Key:        key,
			Status:     "stopped",
			Port:       service.Port,
			NoPort:     service.NoPort,
			PublicHost: service.Public.Hostname,
			Drift:      []string{},
		}

		if record != nil {
			view.Status = record.Status
			view.PID = record.PID
			view.SupervisorPID = record.SupervisorPID
			view.Port = record.Port
			view.NoPort = record.NoPort
			view.RestartCount = record.RestartCount
			view.LastError = record.LastError
			view.LastExitCode = record.LastExitCode
			view.LastReason = record.LastExitReason
		}

		specHash, err := service.SpecHash()
		if err != nil {
			return nil, err
		}
		if record != nil && record.SpecHash != "" && record.SpecHash != specHash {
			view.Drift = append(view.Drift, "spec changed since last start")
		}

		lockHeld, err := LockHeld(m.lockPath(key))
		if err != nil {
			return nil, err
		}
		if (view.Status == "running" || view.Status == "starting") && !lockHeld {
			view.Drift = append(view.Drift, "supervisor lock not held")
		}
		if view.Port > 0 && (view.Status == "running" || view.Status == "starting") && !portListening(view.Port) {
			view.Drift = append(view.Drift, "port not listening")
		}
		if record != nil && record.Port > 0 && record.Port != service.Port {
			view.Drift = append(view.Drift, "wrong port listening")
		}

		healthValue := "unknown"
		if view.Status == "stopped" {
			healthValue = "stopped"
		} else {
			env, err := LoadEnvironment(service)
			if err != nil {
				return nil, err
			}
			cwd, err := ExpandPath(service.CWD)
			if err != nil {
				return nil, err
			}
			result := ProbeHealth(ctx, service, env, cwd, func() bool {
				return lockHeld && processAlive(view.PID)
			})
			healthValue = "unhealthy"
			if result.Healthy {
				healthValue = "healthy"
			} else {
				view.Drift = append(view.Drift, "health check failing")
			}
			if err := m.store.SaveHealth(ctx, HealthRecord{
				Key:        key,
				CheckType:  service.Health.Type,
				Healthy:    result.Healthy,
				Detail:     result.Detail,
				DurationMS: result.Duration.Milliseconds(),
			}); err != nil {
				return nil, err
			}
		}
		view.Health = healthValue

		sort.Strings(view.Drift)
		statuses = append(statuses, view)
	}

	return statuses, nil
}

func (m *Manager) PrintStatus(statuses []StatusView, diffOnly bool) error {
	writer := tabwriter.NewWriter(m.stdout, 0, 8, 2, ' ', 0)
	defer writer.Flush()

	if diffOnly {
		fmt.Fprintln(writer, "KEY\tSTATUS\tDRIFT")
		for _, status := range statuses {
			if len(status.Drift) == 0 {
				continue
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\n", status.Key, status.Status, strings.Join(status.Drift, ", "))
		}
		return nil
	}

	fmt.Fprintln(writer, "KEY\tSTATUS\tHEALTH\tPID\tPORT\tDRIFT")
	for _, status := range statuses {
		drift := "-"
		if len(status.Drift) > 0 {
			drift = strings.Join(status.Drift, ", ")
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%s\n", status.Key, status.Status, status.Health, status.PID, status.Port, drift)
	}
	return nil
}

func (m *Manager) Supervise(ctx context.Context, key string) error {
	supervisor, err := NewSupervisor(m, key)
	if err != nil {
		return err
	}
	return supervisor.Run(ctx)
}

func (m *Manager) lockPath(key string) string {
	return m.paths.Locks + "/" + m.tmux.WindowName(key) + ".lock"
}

func (m *Manager) CleanupSession() error {
	if m.tmux.SessionExists() {
		return exec.Command("tmux", "kill-session", "-t", m.config.TmuxSession).Run()
	}
	return nil
}
