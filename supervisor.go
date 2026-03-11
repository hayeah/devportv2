package devport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const gracefulStopTimeout = 5 * time.Second

type Supervisor struct {
	manager  *Manager
	service  ServiceSpec
	env      Environment
	cwd      string
	lock     *FileLock
	specHash string
	record   ServiceRecord
	log      *slog.Logger
}

func NewSupervisor(ctx context.Context, manager *Manager, key string) (*Supervisor, error) {
	service, err := manager.config.Service(key)
	if err != nil {
		return nil, err
	}

	env, err := LoadEnvironmentWithRuntime(service, manager.runtime)
	if err != nil {
		return nil, err
	}
	cwd, err := manager.runtime.ExpandPath(service.CWD)
	if err != nil {
		return nil, err
	}
	specHash, err := service.SpecHash()
	if err != nil {
		return nil, err
	}

	record := ServiceRecord{
		Key:           key,
		Status:        "starting",
		SpecHash:      specHash,
		SupervisorPID: os.Getpid(),
		Port:          service.Port,
		NoPort:        service.NoPort,
		TmuxWindow:    manager.tmux.WindowName(key),
		StartedAt:     nowUTC(),
	}
	previous, err := manager.store.Service(ctx, key)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		record.RestartCount = previous.RestartCount
	}

	return &Supervisor{
		manager:  manager,
		service:  service,
		env:      env,
		cwd:      cwd,
		lock:     NewFileLock(manager.lockPath(key)),
		specHash: specHash,
		record:   record,
		log:      manager.log.With("service", key),
	}, nil
}

func (s *Supervisor) Run(ctx context.Context) error {
	ok, err := s.lock.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("supervisor already running for %q", s.service.Key)
	}
	defer func() { _ = s.lock.Unlock() }()

	if err := s.persist(ctx); err != nil {
		return err
	}

	command := s.env.ExpandSlice(s.service.Command)
	child := exec.Command(command[0], command[1:]...)
	child.Dir = s.cwd
	child.Env = s.env.Environ()
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := child.Start(); err != nil {
		s.record.Status = "failed"
		s.record.LastError = err.Error()
		s.record.LastExitReason = "start_failed"
		_ = s.persist(ctx)
		_ = s.manager.store.RecordEvent(ctx, s.service.Key, "error", "service_failed", map[string]any{"error": err.Error()})
		return err
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- child.Wait()
	}()

	s.record.PID = child.Process.Pid
	if err := s.persist(ctx); err != nil {
		_ = terminateProcessGroup(child.Process.Pid, gracefulStopTimeout)
		<-waitCh
		return err
	}

	signalCh := make(chan os.Signal, 4)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signalCh)

	readyContext, cancelReady := context.WithTimeout(ctx, s.service.Health.StartupTimeout.Duration()+time.Second)
	defer cancelReady()
	result, readyErr := WaitForStartup(readyContext, s.service, s.env, s.cwd, func() bool {
		return processAlive(s.record.PID)
	})
	if readyErr != nil {
		if err := s.manager.store.SaveHealth(ctx, HealthRecord{
			Key:        s.service.Key,
			CheckType:  s.service.Health.Type,
			Healthy:    false,
			Detail:     result.Detail,
			DurationMS: result.Duration.Milliseconds(),
		}); err != nil {
			s.log.Error("health_check_failed", "error", err)
		}
		s.record.Status = "failed"
		s.record.LastError = readyErr.Error()
		s.record.LastExitReason = "startup_failed"
		if err := terminateProcessGroup(child.Process.Pid, gracefulStopTimeout); err != nil {
			s.log.Error("service_failed", "error", err)
		}
		if waitErr := <-waitCh; waitErr != nil {
			s.record.LastExitCode = exitCode(waitErr)
		}
		_ = s.persist(ctx)
		_ = s.manager.store.RecordEvent(ctx, s.service.Key, "error", "service_failed", map[string]any{"error": readyErr.Error()})
		s.log.Error("service_failed", "error", readyErr, "detail", result.Detail)
		return readyErr
	}

	if err := s.manager.store.SaveHealth(ctx, HealthRecord{
		Key:        s.service.Key,
		CheckType:  s.service.Health.Type,
		Healthy:    true,
		Detail:     result.Detail,
		DurationMS: result.Duration.Milliseconds(),
	}); err != nil {
		s.record.Status = "failed"
		s.record.LastError = err.Error()
		s.record.LastExitReason = "health_persist_failed"
		s.record.LastExitCode = 0
		_ = terminateProcessGroup(child.Process.Pid, gracefulStopTimeout)
		if waitErr := <-waitCh; waitErr != nil {
			s.record.LastExitCode = exitCode(waitErr)
		}
		s.record.PID = 0
		s.record.SupervisorPID = 0
		_ = s.persist(ctx)
		return err
	}
	s.record.Status = "running"
	s.record.LastError = ""
	s.record.LastExitReason = ""
	s.record.LastExitCode = 0
	if err := s.persist(ctx); err != nil {
		s.record.Status = "failed"
		s.record.LastError = err.Error()
		s.record.LastExitReason = "state_persist_failed"
		_ = terminateProcessGroup(child.Process.Pid, gracefulStopTimeout)
		if waitErr := <-waitCh; waitErr != nil {
			s.record.LastExitCode = exitCode(waitErr)
		}
		s.record.PID = 0
		s.record.SupervisorPID = 0
		_ = s.persist(ctx)
		return err
	}
	_ = s.manager.store.RecordEvent(ctx, s.service.Key, "info", "service_started", map[string]any{"pid": s.record.PID, "port": s.record.Port})
	s.log.Info("service_started", "pid", s.record.PID, "port", s.record.Port)

	for {
		select {
		case sig := <-signalCh:
			s.record.Status = "stopped"
			s.record.LastExitReason = "signal:" + sig.String()
			s.record.StoppedAt = nowUTC()
			s.record.LastError = ""
			if err := terminateProcessGroup(child.Process.Pid, gracefulStopTimeout); err != nil {
				s.record.Status = "failed"
				s.record.LastError = err.Error()
			}
			waitErr := <-waitCh
			s.record.LastExitCode = exitCode(waitErr)
			s.record.PID = 0
			s.record.SupervisorPID = 0
			_ = s.persist(ctx)
			_ = s.manager.store.RecordEvent(ctx, s.service.Key, "info", "service_stopped", map[string]any{"reason": s.record.LastExitReason})
			s.log.Info("service_stopped", "reason", s.record.LastExitReason)
			return nil
		case waitErr := <-waitCh:
			s.record.StoppedAt = nowUTC()
			s.record.SupervisorPID = 0
			s.record.PID = 0
			s.record.LastExitCode = exitCode(waitErr)
			if waitErr == nil {
				s.record.Status = "stopped"
				s.record.LastExitReason = "exited"
				s.record.LastError = ""
				_ = s.manager.store.RecordEvent(ctx, s.service.Key, "info", "service_stopped", map[string]any{"reason": "exited"})
				s.log.Info("service_stopped", "reason", "exited")
				return s.persist(ctx)
			}

			s.record.Status = "failed"
			s.record.LastExitReason = "exited"
			s.record.LastError = waitErr.Error()
			_ = s.persist(ctx)
			_ = s.manager.store.RecordEvent(ctx, s.service.Key, "error", "service_failed", map[string]any{"error": waitErr.Error(), "exit_code": s.record.LastExitCode})
			s.log.Error("service_failed", "error", waitErr, "exit_code", s.record.LastExitCode)
			return waitErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Supervisor) persist(ctx context.Context) error {
	return s.manager.store.UpsertService(ctx, s.record)
}

func terminateProcessGroup(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	groupID := -pid
	termErr := syscall.Kill(groupID, syscall.SIGTERM)
	if termErr != nil && !errors.Is(termErr, syscall.ESRCH) {
		return termErr
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	killErr := syscall.Kill(groupID, syscall.SIGKILL)
	if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		return killErr
	}
	time.Sleep(100 * time.Millisecond)
	if processAlive(pid) {
		return fmt.Errorf("process %d is still alive after SIGKILL", pid)
	}
	return nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 1
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		if status.Exited() {
			return status.ExitStatus()
		}
		if status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}
