package devport

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadEnvironmentAndExpansion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("FROM_ENV", "ambient")

	envFile := filepath.Join(dir, ".env.test")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=file\nOVERRIDE=from-file\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	service := ServiceSpec{
		Port:     19173,
		PortEnv:  "VITE_PORT",
		EnvFiles: []string{"~/.env.test"},
	}
	env, err := LoadEnvironment(service)
	if err != nil {
		t.Fatalf("LoadEnvironment: %v", err)
	}

	if value, ok := env.Lookup("FROM_ENV"); !ok || value != "ambient" {
		t.Fatalf("unexpected FROM_ENV value: %q", value)
	}
	if value, ok := env.Lookup("FROM_FILE"); !ok || value != "file" {
		t.Fatalf("unexpected FROM_FILE value: %q", value)
	}
	if value, ok := env.Lookup("PORT"); !ok || value != "19173" {
		t.Fatalf("unexpected PORT value: %q", value)
	}
	if value, ok := env.Lookup("VITE_PORT"); !ok || value != "19173" {
		t.Fatalf("unexpected VITE_PORT value: %q", value)
	}

	expanded := env.ExpandString("${FROM_FILE}:${PORT}:${env:FROM_ENV}:${MISSING}")
	if expanded != "file:19173:ambient:" {
		t.Fatalf("unexpected expanded string: %q", expanded)
	}

	slice := env.ExpandSlice([]string{"${PORT}", "${FROM_FILE}"})
	if !slices.Equal(slice, []string{"19173", "file"}) {
		t.Fatalf("unexpected expanded slice: %v", slice)
	}

	values := env.Environ()
	if !containsStringSlice(values, "PORT=19173") {
		t.Fatalf("expected PORT in environ: %v", values)
	}
}
