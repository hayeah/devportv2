package devport

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "devport.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	record := ServiceRecord{
		Key:            "app/web",
		Status:         "running",
		SpecHash:       "abc123",
		PID:            42,
		SupervisorPID:  43,
		Port:           19173,
		TmuxWindow:     "svc-abc",
		RestartCount:   1,
		LastExitCode:   0,
		LastExitReason: "",
		LastError:      "",
		StartedAt:      nowUTC(),
	}
	if err := store.UpsertService(ctx, record); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}
	if err := store.SaveHealth(ctx, HealthRecord{
		Key:        "app/web",
		CheckType:  "http",
		Healthy:    true,
		Detail:     "ok",
		DurationMS: 12,
	}); err != nil {
		t.Fatalf("SaveHealth: %v", err)
	}
	if err := store.RecordEvent(ctx, "app/web", "info", "service_started", map[string]any{"pid": 42}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	loaded, err := store.Service(ctx, "app/web")
	if err != nil {
		t.Fatalf("Service: %v", err)
	}
	if loaded == nil || loaded.PID != 42 || loaded.Status != "running" {
		t.Fatalf("unexpected loaded service: %+v", loaded)
	}

	health, err := store.Health(ctx, "app/web")
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health == nil || !health.Healthy || health.CheckType != "http" {
		t.Fatalf("unexpected health row: %+v", health)
	}

	services, err := store.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service row, got %d", len(services))
	}

	if err := store.DeleteService(ctx, "app/web"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	loaded, err = store.Service(ctx, "app/web")
	if err != nil {
		t.Fatalf("Service after delete: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected service to be deleted")
	}
}
