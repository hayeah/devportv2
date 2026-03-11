package devport

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("DEVPORT_CONFIG", "~/custom/devport.toml")
	t.Setenv("DEVPORT_STATE_DIR", "~/state/devport")

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
