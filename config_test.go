package devport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigAndIngressRules(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "devport.toml")
	configText := `
version = 2
port_range = { start = 19000, end = 19010 }
tmux_session = "devport-test"

[service."app/web"]
cwd = "~/src/web"
command = ["devserver", "--port", "${PORT}"]
port = 19001
port_env = "VITE_PORT"
restart = "never"
env_files = ["~/.env.test"]

[service."app/web".health]
type = "http"
url = "http://127.0.0.1:${PORT}/"
expect_status = [200, 204]
startup_timeout = "12s"

[service."app/web".public]
hostname = "web.example.test"

[service."jobs/worker"]
cwd = "~/src/worker"
command = ["worker"]
no_port = true
restart = "never"

[service."jobs/worker".health]
type = "process"
`
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if config.TmuxSession != "devport-test" {
		t.Fatalf("unexpected tmux session: %s", config.TmuxSession)
	}

	keys, err := config.ServiceKeys(nil)
	if err != nil {
		t.Fatalf("ServiceKeys: %v", err)
	}
	if len(keys) != 2 || keys[0] != "app/web" || keys[1] != "jobs/worker" {
		t.Fatalf("unexpected service keys: %v", keys)
	}

	web, err := config.Service("app/web")
	if err != nil {
		t.Fatalf("Service: %v", err)
	}
	if web.Health.StartupTimeout.Duration().Seconds() != 12 {
		t.Fatalf("unexpected startup timeout: %s", web.Health.StartupTimeout.Duration())
	}

	hash1, err := web.SpecHash()
	if err != nil {
		t.Fatalf("SpecHash: %v", err)
	}
	hash2, err := web.SpecHash()
	if err != nil {
		t.Fatalf("SpecHash second: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("expected stable hash, got %s != %s", hash1, hash2)
	}

	rules, err := config.IngressRules(nil)
	if err != nil {
		t.Fatalf("IngressRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 ingress rules, got %d", len(rules))
	}
	if rules[0].Hostname != "web.example.test" || rules[0].Service != "http://localhost:19001" {
		t.Fatalf("unexpected first ingress rule: %+v", rules[0])
	}
	if rules[1].Service != "http_status:404" {
		t.Fatalf("unexpected catch-all ingress rule: %+v", rules[1])
	}
}

func TestLoadConfigValidationErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "bad.toml")

	tests := []struct {
		name    string
		config  string
		message string
	}{
		{
			name: "duplicate ports",
			config: `
version = 2
[service."a"]
cwd = "/tmp"
command = ["a"]
port = 19001
[service."a".health]
type = "process"

[service."b"]
cwd = "/tmp"
command = ["b"]
port = 19001
[service."b".health]
type = "process"
`,
			message: `port 19001 already used by`,
		},
		{
			name: "port env requires port",
			config: `
version = 2
[service."a"]
cwd = "/tmp"
command = ["a"]
no_port = true
port_env = "PORT"
[service."a".health]
type = "process"
`,
			message: "port_env requires port to be set",
		},
		{
			name: "missing port mode",
			config: `
version = 2
[service."a"]
cwd = "/tmp"
command = ["a"]
[service."a".health]
type = "process"
`,
			message: "must set either port or no_port = true",
		},
		{
			name: "invalid public hostname on no-port service",
			config: `
version = 2
[service."a"]
cwd = "/tmp"
command = ["a"]
no_port = true
[service."a".health]
type = "process"
[service."a".public]
hostname = "bad.example.test"
`,
			message: "public.hostname requires a port-bearing service",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(test.config), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := LoadConfig(configPath)
			if err == nil || err.Error() == "" {
				t.Fatalf("expected validation error")
			}
			if !containsString(err.Error(), test.message) {
				t.Fatalf("expected error to contain %q, got %v", test.message, err)
			}
		})
	}
}
