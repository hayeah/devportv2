package devport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type ServiceRecord struct {
	Key            string
	Status         string
	SpecHash       string
	PID            int
	SupervisorPID  int
	Port           int
	NoPort         bool
	TmuxWindow     string
	RestartCount   int
	LastExitCode   int
	LastExitReason string
	LastError      string
	StartedAt      string
	StoppedAt      string
	UpdatedAt      string
}

type HealthRecord struct {
	Key        string
	CheckType  string
	Healthy    bool
	Detail     string
	CheckedAt  string
	DurationMS int64
}

func OpenStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	queries := []string{
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE IF NOT EXISTS services (
			key TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			spec_hash TEXT NOT NULL,
			pid INTEGER NOT NULL DEFAULT 0,
			supervisor_pid INTEGER NOT NULL DEFAULT 0,
			port INTEGER NOT NULL DEFAULT 0,
			no_port INTEGER NOT NULL DEFAULT 0,
			tmux_window TEXT NOT NULL DEFAULT '',
			restart_count INTEGER NOT NULL DEFAULT 0,
			last_exit_code INTEGER NOT NULL DEFAULT 0,
			last_exit_reason TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			stopped_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS health_checks (
			key TEXT PRIMARY KEY,
			check_type TEXT NOT NULL,
			healthy INTEGER NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			checked_at TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			service_key TEXT NOT NULL,
			level TEXT NOT NULL,
			event TEXT NOT NULL,
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		);`,
	}

	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertService(ctx context.Context, record ServiceRecord) error {
	record.UpdatedAt = nowUTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO services (
			key, status, spec_hash, pid, supervisor_pid, port, no_port, tmux_window, restart_count,
			last_exit_code, last_exit_reason, last_error, started_at, stopped_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			status = excluded.status,
			spec_hash = excluded.spec_hash,
			pid = excluded.pid,
			supervisor_pid = excluded.supervisor_pid,
			port = excluded.port,
			no_port = excluded.no_port,
			tmux_window = excluded.tmux_window,
			restart_count = excluded.restart_count,
			last_exit_code = excluded.last_exit_code,
			last_exit_reason = excluded.last_exit_reason,
			last_error = excluded.last_error,
			started_at = excluded.started_at,
			stopped_at = excluded.stopped_at,
			updated_at = excluded.updated_at
	`, record.Key, record.Status, record.SpecHash, record.PID, record.SupervisorPID, record.Port, boolToInt(record.NoPort),
		record.TmuxWindow, record.RestartCount, record.LastExitCode, record.LastExitReason, record.LastError,
		record.StartedAt, record.StoppedAt, record.UpdatedAt)
	return err
}

func (s *Store) SaveHealth(ctx context.Context, record HealthRecord) error {
	record.CheckedAt = nowUTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO health_checks (key, check_type, healthy, detail, checked_at, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			check_type = excluded.check_type,
			healthy = excluded.healthy,
			detail = excluded.detail,
			checked_at = excluded.checked_at,
			duration_ms = excluded.duration_ms
	`, record.Key, record.CheckType, boolToInt(record.Healthy), record.Detail, record.CheckedAt, record.DurationMS)
	return err
}

func (s *Store) RecordEvent(ctx context.Context, serviceKey, level, event string, data map[string]any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO events (service_key, level, event, data_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, serviceKey, level, event, string(payload), nowUTC())
	return err
}

func (s *Store) Service(ctx context.Context, key string) (*ServiceRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT key, status, spec_hash, pid, supervisor_pid, port, no_port, tmux_window,
		       restart_count, last_exit_code, last_exit_reason, last_error, started_at, stopped_at, updated_at
		FROM services WHERE key = ?
	`, key)

	var record ServiceRecord
	var noPort int
	if err := row.Scan(&record.Key, &record.Status, &record.SpecHash, &record.PID, &record.SupervisorPID, &record.Port, &noPort,
		&record.TmuxWindow, &record.RestartCount, &record.LastExitCode, &record.LastExitReason, &record.LastError,
		&record.StartedAt, &record.StoppedAt, &record.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	record.NoPort = noPort == 1
	return &record, nil
}

func (s *Store) Health(ctx context.Context, key string) (*HealthRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT key, check_type, healthy, detail, checked_at, duration_ms
		FROM health_checks WHERE key = ?
	`, key)

	var record HealthRecord
	var healthy int
	if err := row.Scan(&record.Key, &record.CheckType, &healthy, &record.Detail, &record.CheckedAt, &record.DurationMS); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	record.Healthy = healthy == 1
	return &record, nil
}

func (s *Store) Services(ctx context.Context) ([]ServiceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, status, spec_hash, pid, supervisor_pid, port, no_port, tmux_window,
		       restart_count, last_exit_code, last_exit_reason, last_error, started_at, stopped_at, updated_at
		FROM services
		ORDER BY key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []ServiceRecord{}
	for rows.Next() {
		var record ServiceRecord
		var noPort int
		if err := rows.Scan(&record.Key, &record.Status, &record.SpecHash, &record.PID, &record.SupervisorPID, &record.Port, &noPort,
			&record.TmuxWindow, &record.RestartCount, &record.LastExitCode, &record.LastExitReason, &record.LastError,
			&record.StartedAt, &record.StoppedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		record.NoPort = noPort == 1
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) DeleteService(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM services WHERE key = ?`, key)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM health_checks WHERE key = ?`, key)
	return err
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
