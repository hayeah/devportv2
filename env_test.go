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
	// port_env overrides PORT — only VITE_PORT should be set
	if _, ok := env.Lookup("PORT"); ok {
		t.Fatalf("PORT should not be set when port_env is specified")
	}
	if value, ok := env.Lookup("VITE_PORT"); !ok || value != "19173" {
		t.Fatalf("unexpected VITE_PORT value: %q", value)
	}

	expanded := env.ExpandString("${FROM_FILE}:${VITE_PORT}:${FROM_ENV}:${MISSING}")
	if expanded != "file:19173:ambient:" {
		t.Fatalf("unexpected expanded string: %q", expanded)
	}

	slice := env.ExpandSlice([]string{"${VITE_PORT}", "${FROM_FILE}"})
	if !slices.Equal(slice, []string{"19173", "file"}) {
		t.Fatalf("unexpected expanded slice: %v", slice)
	}

	values := env.Environ()
	if !containsStringSlice(values, "VITE_PORT=19173") {
		t.Fatalf("expected VITE_PORT in environ: %v", values)
	}
	if containsStringSlice(values, "PORT=19173") {
		t.Fatalf("PORT should not be in environ when port_env is specified")
	}
}

func TestLoadEnvironmentDefaultPort(t *testing.T) {
	service := ServiceSpec{
		Port: 19200,
	}
	env, err := LoadEnvironment(service)
	if err != nil {
		t.Fatalf("LoadEnvironment: %v", err)
	}
	if value, ok := env.Lookup("PORT"); !ok || value != "19200" {
		t.Fatalf("expected PORT=19200, got %q", value)
	}
}

func TestLoadEnvironmentMultiPortEnv(t *testing.T) {
	service := ServiceSpec{
		Port:    19300,
		PortEnv: "VITE_PORT:PORT",
	}
	env, err := LoadEnvironment(service)
	if err != nil {
		t.Fatalf("LoadEnvironment: %v", err)
	}
	if value, ok := env.Lookup("VITE_PORT"); !ok || value != "19300" {
		t.Fatalf("expected VITE_PORT=19300, got %q", value)
	}
	if value, ok := env.Lookup("PORT"); !ok || value != "19300" {
		t.Fatalf("expected PORT=19300, got %q", value)
	}
}
