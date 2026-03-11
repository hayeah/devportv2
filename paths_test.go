package devport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	oldHome := os.Getenv("HOME")
	oldConfig := os.Getenv("DEVPORT_CONFIG")
	oldState := os.Getenv("DEVPORT_STATE_DIR")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEVPORT_CONFIG", oldConfig)
		_ = os.Setenv("DEVPORT_STATE_DIR", oldState)
	})

	if err := os.Setenv("HOME", dir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("DEVPORT_CONFIG", "~/custom/devport.toml"); err != nil {
		t.Fatalf("set DEVPORT_CONFIG: %v", err)
	}
	if err := os.Setenv("DEVPORT_STATE_DIR", "~/state/devport"); err != nil {
		t.Fatalf("set DEVPORT_STATE_DIR: %v", err)
	}

	paths, err := ResolvePaths("")
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if paths.Config != filepath.Join(dir, "custom", "devport.toml") {
		t.Fatalf("unexpected config path: %s", paths.Config)
	}
	if paths.State != filepath.Join(dir, "state", "devport") {
		t.Fatalf("unexpected state dir: %s", paths.State)
	}
	if paths.DB != filepath.Join(dir, "state", "devport", "devport.db") {
		t.Fatalf("unexpected db path: %s", paths.DB)
	}
}

func TestExpandPathRejectsEmpty(t *testing.T) {
	if _, err := ExpandPath(""); err == nil {
		t.Fatalf("expected error for empty path")
	}
}
