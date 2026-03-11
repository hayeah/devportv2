package devport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

type Manager struct {
	runtime    *Runtime
	store      *Store
	tmux       *Tmux
	log        *slog.Logger
	executable string
}

type StatusView struct {
	Key           string  `json:"key"`
	Status        string  `json:"status"`
	Health        string  `json:"health"`
	PID           int     `json:"pid"`
	SupervisorPID int     `json:"supervisor_pid"`
	Port          int     `json:"port"`
	NoPort        bool    `json:"no_port"`
	RestartCount  int     `json:"restart_count"`
	PublicHost    string  `json:"public_hostname,omitempty"`
	Issues        []Issue `json:"issues"`
	LastError     string  `json:"last_error,omitempty"`
	LastExitCode  int     `json:"last_exit_code,omitempty"`
	LastReason    string  `json:"last_reason,omitempty"`
	LastChecked   string  `json:"last_checked,omitempty"`
	CheckDuration int64   `json:"check_duration_ms,omitempty"`
}

type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type IngressDocument struct {
	Ingress []IngressRule `json:"ingress"`
}

type serviceStatus struct {
	key      string
	service  ServiceSpec
	record   *ServiceRecord
	view     StatusView
	lockHeld bool
}

func NewManager(explicitConfig string, stdout, stderr io.Writer) (*Manager, error) {
	return NewManagerWithRuntime(RuntimeConfig{ConfigPath: explicitConfig}, stdout, stderr)
}

func NewManagerWithRuntime(runtime RuntimeConfig, stdout, stderr io.Writer) (*Manager, error) {
	manager, err := InitializeManager(runtime, ManagerIO{Stdout: stdout, Stderr: stderr})
	if err != nil {
		return nil, err
	}
	manager.log.Info("spec_loaded", "config", manager.runtime.Paths.Config, "services", len(manager.runtime.Spec.Services))
	return manager, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	return m.store.Close()
}

func (m *Manager) Start(ctx context.Context, key string) error {
	unlock, err := m.lockOperation(key)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.Service(ctx, key)
	if err != nil {
		return err
	}
	if record != nil {
		record.RestartCount = 0
		if err := m.store.UpsertService(ctx, *record); err != nil {
			return err
		}
	}

	return m.startLocked(ctx, key)
}

func (m *Manager) startLocked(ctx context.Context, key string) error {
	service, err := m.runtime.Spec.Service(key)
	if err != nil {
		return err
	}
	record, err := m.store.Service(ctx, key)
	if err != nil {
		return err
	}

	lockHeld, err := LockHeld(m.lockPath(key))
	if err != nil {
		return err
	}
	if lockHeld {
		return fmt.Errorf("service %q is already running", key)
	}
	if record != nil && record.PID > 0 && processAlive(record.PID) {
		if err := terminateProcessGroup(record.PID, gracefulStopTimeout); err != nil {
			return fmt.Errorf("clean up stale process for %q: %w", key, err)
		}
		_ = m.tmux.KillWindow(m.windowName(record))
		record.Status = "failed"
		record.PID = 0
		record.SupervisorPID = 0
		record.LastExitReason = "stale_process_reaped"
		if record.LastError == "" {
			record.LastError = "stale process reaped before start"
		}
		record.StoppedAt = nowUTC()
		if err := m.store.UpsertService(ctx, *record); err != nil {
			return err
		}
	}
	if record != nil {
		record.Status = "stopped"
		record.PID = 0
		record.SupervisorPID = 0
		record.LastError = ""
		record.StoppedAt = nowUTC()
		if err := m.store.UpsertService(ctx, *record); err != nil {
			return err
		}
	}
	if err := ensurePortAvailable(service.Port); err != nil {
		return err
	}

	window := m.tmux.WindowName(key)
	runtimeJSON, err := m.runtime.Config.MarshalJSONValue()
	if err != nil {
		return err
	}
	command := []string{
		m.executable,
		"supervise",
		"--file", m.runtime.Paths.Config,
		"--key", key,
		"--runtime-json", runtimeJSON,
	}
	if err := m.tmux.Start(window, m.runtime.Config.TmuxEnvironment(), command); err != nil {
		return err
	}

	return m.waitForStart(ctx, key, service.Health.StartupTimeout.Duration()+3*time.Second)
}

func (m *Manager) waitForStart(ctx context.Context, key string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		record, err := m.store.Service(ctx, key)
		if err != nil {
			return err
		}
		lockHeld, err := LockHeld(m.lockPath(key))
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
			case "starting":
				if !lockHeld {
					if record.LastError != "" {
						return errors.New(record.LastError)
					}
					return fmt.Errorf("service %q failed during startup", key)
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for service %q to report ready", key)
}

func (m *Manager) Stop(ctx context.Context, key, reason string) error {
	unlock, err := m.lockOperation(key)
	if err != nil {
		return err
	}
	defer unlock()

	return m.stopLocked(ctx, key, reason)
}

func (m *Manager) stopLocked(ctx context.Context, key, reason string) error {
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
	if record.PID > 0 && processAlive(record.PID) {
		if err := terminateProcessGroup(record.PID, gracefulStopTimeout); err != nil {
			return err
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
	unlock, err := m.lockOperation(key)
	if err != nil {
		return err
	}
	defer unlock()

	if err := m.stopLocked(ctx, key, "restart"); err != nil {
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
	return m.startLocked(ctx, key)
}

func (m *Manager) Up(ctx context.Context, keys []string) error {
	selected, err := m.runtime.Spec.ServiceKeys(keys)
	if err != nil {
		return err
	}

	failures := []string{}
	for _, key := range selected {
		record, err := m.store.Service(ctx, key)
		if err != nil {
			return err
		}
		lockHeld, err := LockHeld(m.lockPath(key))
		if err != nil {
			return err
		}
		if record != nil && lockHeld && (record.Status == "running" || record.Status == "starting") {
			continue
		}
		if err := m.Start(ctx, key); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func (m *Manager) Down(ctx context.Context, keys []string) error {
	selected, err := m.runtime.Spec.ServiceKeys(keys)
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

func (m *Manager) ServiceKeys(keys []string) ([]string, error) {
	return m.runtime.Spec.ServiceKeys(keys)
}

func (m *Manager) Attach(ctx context.Context, key string) error {
	if _, err := m.runtime.Spec.Service(key); err != nil {
		return err
	}
	window := m.tmux.WindowName(key)
	record, err := m.store.Service(ctx, key)
	if err != nil {
		return err
	}
	if record != nil && record.TmuxWindow != "" {
		window = record.TmuxWindow
	}
	if !m.tmux.WindowExists(window) {
		return fmt.Errorf("service %q has no tmux window", key)
	}
	return m.tmux.AttachWindow(window)
}

func (m *Manager) FreePort(keys []string) (int, error) {
	if m.runtime.Spec.PortRange.Start == 0 || m.runtime.Spec.PortRange.End == 0 {
		return 0, fmt.Errorf("config must define port_range")
	}

	used := map[int]bool{}
	selected, err := m.runtime.Spec.ServiceKeys(keys)
	if err != nil && len(keys) > 0 {
		return 0, err
	}
	if len(keys) == 0 {
		selected, _ = m.runtime.Spec.ServiceKeys(nil)
	}
	for _, key := range selected {
		service := m.runtime.Spec.Services[key]
		if service.Port > 0 {
			used[service.Port] = true
		}
	}

	for port := m.runtime.Spec.PortRange.Start; port <= m.runtime.Spec.PortRange.End; port++ {
		if used[port] {
			continue
		}
		if !portListening(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in range %d-%d", m.runtime.Spec.PortRange.Start, m.runtime.Spec.PortRange.End)
}

func (m *Manager) Ingress(keys []string) ([]byte, error) {
	rules, err := m.runtime.Spec.IngressRules(keys)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(IngressDocument{Ingress: rules}, "", "  ")
}

func (m *Manager) Status(ctx context.Context, keys []string) ([]StatusView, error) {
	selected, err := m.runtime.Spec.ServiceKeys(keys)
	if err != nil {
		return nil, err
	}

	statuses := make([]StatusView, 0, len(selected))
	for _, key := range selected {
		status, err := m.statusForKey(ctx, key)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}

func (m *Manager) statusForKey(ctx context.Context, key string) (StatusView, error) {
	service := m.runtime.Spec.Services[key]
	record, err := m.store.Service(ctx, key)
	if err != nil {
		return StatusView{}, err
	}

	status := serviceStatus{
		key:     key,
		service: service,
		record:  record,
		view:    m.baseStatusView(key, service, record),
	}
	if err := m.applySpecIssues(&status); err != nil {
		return StatusView{}, err
	}
	if err := m.reconcileSupervisor(ctx, &status); err != nil {
		return StatusView{}, err
	}
	m.applyPortIssues(&status)
	if err := m.updateHealthStatus(ctx, &status); err != nil {
		return StatusView{}, err
	}

	if status.view.Status == "failed" {
		summary := "service failed"
		if strings.TrimSpace(status.view.LastError) != "" {
			summary = status.view.LastError
		}
		addIssue(&status.view, Issue{
			Code:     "service_failed",
			Severity: "error",
			Summary:  summary,
		})
	}
	return status.view, nil
}

func (m *Manager) baseStatusView(key string, service ServiceSpec, record *ServiceRecord) StatusView {
	view := StatusView{
		Key:        key,
		Status:     "stopped",
		Port:       service.Port,
		NoPort:     service.NoPort,
		PublicHost: service.Public.Hostname,
		Issues:     []Issue{},
	}
	if record == nil {
		return view
	}

	view.Status = record.Status
	view.PID = record.PID
	view.SupervisorPID = record.SupervisorPID
	view.Port = record.Port
	view.NoPort = record.NoPort
	view.RestartCount = record.RestartCount
	view.LastError = record.LastError
	view.LastExitCode = record.LastExitCode
	view.LastReason = record.LastExitReason
	return view
}

func (m *Manager) applySpecIssues(status *serviceStatus) error {
	specHash, err := status.service.SpecHash()
	if err != nil {
		return err
	}
	if status.record != nil && status.record.SpecHash != "" && status.record.SpecHash != specHash {
		addIssue(&status.view, Issue{
			Code:     "spec_changed_since_last_start",
			Severity: "warning",
			Summary:  "spec changed since last start",
		})
	}
	return nil
}

func (m *Manager) reconcileSupervisor(ctx context.Context, status *serviceStatus) error {
	lockHeld, err := LockHeld(m.lockPath(status.key))
	if err != nil {
		return err
	}
	status.lockHeld = lockHeld
	if !runningStatus(status.view.Status) || lockHeld {
		return nil
	}

	status.view.Status = "failed"
	addIssue(&status.view, Issue{
		Code:     "supervisor_lock_not_held",
		Severity: "error",
		Summary:  "supervisor lock not held",
	})
	if status.record == nil {
		return nil
	}

	status.record.Status = "failed"
	status.record.SupervisorPID = 0
	if status.record.LastError == "" {
		status.record.LastError = "supervisor lock not held"
	}
	status.record.LastExitReason = "supervisor_missing"
	status.record.StoppedAt = nowUTC()
	if err := m.store.UpsertService(ctx, *status.record); err != nil {
		return err
	}
	status.view.SupervisorPID = status.record.SupervisorPID
	status.view.LastError = status.record.LastError
	status.view.LastReason = status.record.LastExitReason
	return nil
}

func (m *Manager) applyPortIssues(status *serviceStatus) {
	if status.view.Port > 0 && runningStatus(status.view.Status) && !portListening(status.view.Port) {
		addIssue(&status.view, Issue{
			Code:     "port_not_listening",
			Severity: "error",
			Summary:  "port not listening",
		})
	}
	if status.record != nil && status.record.Port > 0 && status.record.Port != status.service.Port {
		addIssue(&status.view, Issue{
			Code:     "wrong_port_listening",
			Severity: "error",
			Summary:  "wrong port listening",
		})
	}
}

func (m *Manager) updateHealthStatus(ctx context.Context, status *serviceStatus) error {
	switch {
	case status.view.Status == "stopped":
		status.view.Health = "stopped"
		return nil
	case status.view.Status == "failed" && !status.lockHeld:
		status.view.Health = "unhealthy"
		status.view.LastChecked = nowUTC()
		return m.store.SaveHealth(ctx, HealthRecord{
			Key:        status.key,
			CheckType:  status.service.Health.Type,
			Healthy:    false,
			Detail:     "supervisor lock not held",
			DurationMS: 0,
		})
	default:
		result, err := m.probeServiceHealth(ctx, status)
		if err != nil {
			return err
		}
		status.view.Health = "unhealthy"
		if result.Healthy {
			status.view.Health = "healthy"
		} else {
			addIssue(&status.view, Issue{
				Code:     "health_check_failing",
				Severity: "error",
				Summary:  "health check failing",
			})
		}
		status.view.LastChecked = nowUTC()
		status.view.CheckDuration = result.Duration.Milliseconds()
		return m.store.SaveHealth(ctx, HealthRecord{
			Key:        status.key,
			CheckType:  status.service.Health.Type,
			Healthy:    result.Healthy,
			Detail:     result.Detail,
			DurationMS: result.Duration.Milliseconds(),
		})
	}
}

func (m *Manager) probeServiceHealth(ctx context.Context, status *serviceStatus) (HealthResult, error) {
	env, err := LoadEnvironmentWithRuntime(status.service, m.runtime.Config)
	if err != nil {
		return HealthResult{}, err
	}
	cwd, err := m.runtime.Config.ExpandPath(status.service.CWD)
	if err != nil {
		return HealthResult{}, err
	}
	return ProbeHealth(ctx, status.service, env, cwd, func() bool {
		held, err := LockHeld(m.lockPath(status.key))
		if err != nil {
			return false
		}
		return held && processAlive(status.view.PID)
	}), nil
}

func runningStatus(status string) bool {
	return status == "running" || status == "starting"
}

func addIssue(view *StatusView, issue Issue) {
	for _, existing := range view.Issues {
		if existing.Code == issue.Code && existing.Summary == issue.Summary {
			return
		}
	}
	view.Issues = append(view.Issues, issue)
}

func FilterStatusesWithIssues(statuses []StatusView) []StatusView {
	filtered := make([]StatusView, 0, len(statuses))
	for _, status := range statuses {
		if len(status.Issues) == 0 {
			continue
		}
		filtered = append(filtered, status)
	}
	return filtered
}

func issueSummaries(status StatusView) string {
	if len(status.Issues) == 0 {
		return "-"
	}
	values := make([]string, 0, len(status.Issues))
	for _, issue := range status.Issues {
		values = append(values, issue.Summary)
	}
	return strings.Join(values, ", ")
}

func (m *Manager) PrintStatus(statuses []StatusView) error {
	writer := tabwriter.NewWriter(m.runtime.IO.Stdout, 0, 8, 2, ' ', 0)
	defer writer.Flush()

	fmt.Fprintln(writer, "KEY\tSTATUS\tHEALTH\tPID\tPORT\tRESTARTS\tCHECK\tISSUES")
	for _, status := range statuses {
		check := "-"
		if status.LastChecked != "" {
			check = fmt.Sprintf("%dms ago", status.CheckDuration)
			if t, err := time.Parse(time.RFC3339Nano, status.LastChecked); err == nil {
				ago := time.Since(t).Truncate(time.Second)
				check = fmt.Sprintf("%dms (%s ago)", status.CheckDuration, ago)
			}
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n", status.Key, status.Status, status.Health, status.PID, status.Port, status.RestartCount, check, issueSummaries(status))
	}
	return nil
}

func (m *Manager) PrintDoctor(statuses []StatusView) error {
	writer := tabwriter.NewWriter(m.runtime.IO.Stdout, 0, 8, 2, ' ', 0)
	defer writer.Flush()

	filtered := FilterStatusesWithIssues(statuses)
	if len(filtered) == 0 {
		_, err := fmt.Fprintln(writer, "No issues found.")
		return err
	}

	fmt.Fprintln(writer, "KEY\tSTATUS\tHEALTH\tISSUES")
	for _, status := range filtered {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", status.Key, status.Status, status.Health, issueSummaries(status))
	}
	return nil
}

func (m *Manager) Supervise(ctx context.Context, key string) error {
	supervisor, err := NewSupervisor(ctx, m, key)
	if err != nil {
		return err
	}
	return supervisor.Run(ctx)
}

func (m *Manager) lockPath(key string) string {
	return m.runtime.Paths.Locks + "/" + m.tmux.WindowName(key) + ".lock"
}

func (m *Manager) operationLockPath(key string) string {
	return m.runtime.Paths.Locks + "/" + m.tmux.WindowName(key) + ".op.lock"
}

func (m *Manager) windowName(record *ServiceRecord) string {
	if record != nil && record.TmuxWindow != "" {
		return record.TmuxWindow
	}
	if record != nil {
		return m.tmux.WindowName(record.Key)
	}
	return ""
}

func (m *Manager) lockOperation(key string) (func(), error) {
	lock := NewFileLock(m.operationLockPath(key))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("service %q is busy", key)
	}
	return func() {
		_ = lock.Unlock()
	}, nil
}

func (m *Manager) CleanupSession() error {
	if m.tmux.SessionExists() {
		return exec.Command("tmux", "kill-session", "-t", m.runtime.Spec.TmuxSession).Run()
	}
	return nil
}
