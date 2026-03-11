package devport

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsHonorsRuntimeOverrides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	paths, err := ResolvePathsWithRuntime(RuntimeConfig{
		HomeDir:    dir,
		ConfigPath: "~/custom/devport.toml",
		RootDir:    "~/state/devport",
	})
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

func TestResolvePathsHonorsDevportRootEnv(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	paths, err := ResolvePathsWithRuntime(RuntimeConfig{
		HomeDir: dir,
		Env: map[string]string{
			"DEVPORT_ROOT": "~/sandbox/devport",
		},
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if paths.Config != filepath.Join(dir, "sandbox", "devport", "devport.toml") {
		t.Fatalf("unexpected config path: %s", paths.Config)
	}
	if paths.State != filepath.Join(dir, "sandbox", "devport") {
		t.Fatalf("unexpected state dir: %s", paths.State)
	}
	if paths.DB != filepath.Join(dir, "sandbox", "devport", "devport.db") {
		t.Fatalf("unexpected db path: %s", paths.DB)
	}
}

func TestExpandPathRejectsEmpty(t *testing.T) {
	t.Parallel()

	if _, err := ExpandPath(""); err == nil {
		t.Fatalf("expected error for empty path")
	}
}
