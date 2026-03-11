package devport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

type Config struct {
	Version     int                    `toml:"version"`
	PortRange   PortRange              `toml:"port_range"`
	TmuxSession string                 `toml:"tmux_session"`
	Services    map[string]ServiceSpec `toml:"service"`
}

type PortRange struct {
	Start int `toml:"start"`
	End   int `toml:"end"`
}

type ServiceSpec struct {
	Key      string     `toml:"-"`
	CWD      string     `toml:"cwd"`
	Command  []string   `toml:"command"`
	Port     int        `toml:"port"`
	NoPort   bool       `toml:"no_port"`
	PortEnv  string     `toml:"port_env"`
	EnvFiles []string   `toml:"env_files"`
	Restart  string     `toml:"restart"`
	Health   HealthSpec `toml:"health"`
	Public   PublicSpec `toml:"public"`
}

type HealthSpec struct {
	Type           string   `toml:"type"`
	URL            string   `toml:"url"`
	ExpectStatus   []int    `toml:"expect_status"`
	Command        []string `toml:"command"`
	StartupTimeout Duration `toml:"startup_timeout"`
}

type PublicSpec struct {
	Hostname string `toml:"hostname"`
}

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := config.normalize(); err != nil {
		return nil, err
	}
	return &config, nil
}

func (c *Config) normalize() error {
	if c.Version != 2 {
		return fmt.Errorf("config version must be 2")
	}
	if c.TmuxSession == "" {
		c.TmuxSession = "devport"
	}
	if c.PortRange.Start > 0 && c.PortRange.End > 0 && c.PortRange.Start > c.PortRange.End {
		return fmt.Errorf("port_range.start must be <= port_range.end")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("config must define at least one service")
	}

	ports := map[int]string{}
	for key, service := range c.Services {
		service.Key = key
		if err := service.normalize(); err != nil {
			return fmt.Errorf("service %q: %w", key, err)
		}
		if service.Port > 0 {
			if other, exists := ports[service.Port]; exists {
				return fmt.Errorf("service %q: port %d already used by %q", key, service.Port, other)
			}
			ports[service.Port] = key
		}
		c.Services[key] = service
	}

	return nil
}

func (s *ServiceSpec) normalize() error {
	if s.CWD == "" {
		return fmt.Errorf("cwd is required")
	}
	if len(s.Command) == 0 {
		return fmt.Errorf("command is required")
	}
	if s.Port > 0 && s.NoPort {
		return fmt.Errorf("port and no_port cannot both be set")
	}
	if s.Port == 0 && !s.NoPort {
		return fmt.Errorf("must set either port or no_port = true")
	}
	if s.PortEnv != "" && s.Port == 0 {
		return fmt.Errorf("port_env requires port to be set")
	}
	if s.Restart == "" {
		s.Restart = "never"
	}
	if s.Restart != "never" {
		return fmt.Errorf("unsupported restart policy %q", s.Restart)
	}
	if err := s.Health.normalize(s.NoPort); err != nil {
		return err
	}
	if s.Public.Hostname != "" {
		parsed, err := url.Parse("https://" + s.Public.Hostname)
		if err != nil || parsed.Hostname() == "" {
			return fmt.Errorf("public.hostname must be a valid hostname")
		}
		if s.NoPort {
			return fmt.Errorf("public.hostname requires a port-bearing service")
		}
	}
	return nil
}

func (h *HealthSpec) normalize(noPort bool) error {
	if h.Type == "" {
		return fmt.Errorf("health block is required")
	}
	if h.StartupTimeout.Duration() == 0 {
		h.StartupTimeout = Duration(10 * time.Second)
	}

	switch h.Type {
	case "none":
		return nil
	case "process":
		return nil
	case "http":
		if h.URL == "" {
			return fmt.Errorf("health.url is required for type=http")
		}
		if len(h.ExpectStatus) == 0 {
			h.ExpectStatus = []int{200}
		}
		return nil
	case "command":
		if len(h.Command) == 0 {
			return fmt.Errorf("health.command is required for type=command")
		}
		return nil
	default:
		return fmt.Errorf("unsupported health.type %q", h.Type)
	}
}

func (c *Config) Service(key string) (ServiceSpec, error) {
	service, ok := c.Services[key]
	if !ok {
		return ServiceSpec{}, fmt.Errorf("service %q not found in spec", key)
	}
	return service, nil
}

func (c *Config) ServiceKeys(filter []string) ([]string, error) {
	if len(filter) == 0 {
		keys := make([]string, 0, len(c.Services))
		for key := range c.Services {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return keys, nil
	}

	keys := make([]string, 0, len(filter))
	seen := map[string]bool{}
	for _, key := range filter {
		if seen[key] {
			continue
		}
		if _, ok := c.Services[key]; !ok {
			return nil, fmt.Errorf("service %q not found in spec", key)
		}
		seen[key] = true
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (s ServiceSpec) SpecHash() (string, error) {
	normalized := struct {
		Key      string     `json:"key"`
		CWD      string     `json:"cwd"`
		Command  []string   `json:"command"`
		Port     int        `json:"port"`
		NoPort   bool       `json:"no_port"`
		PortEnv  string     `json:"port_env"`
		EnvFiles []string   `json:"env_files"`
		Restart  string     `json:"restart"`
		Health   HealthSpec `json:"health"`
		Public   PublicSpec `json:"public"`
	}{
		Key:      s.Key,
		CWD:      s.CWD,
		Command:  s.Command,
		Port:     s.Port,
		NoPort:   s.NoPort,
		PortEnv:  s.PortEnv,
		EnvFiles: append([]string(nil), s.EnvFiles...),
		Restart:  s.Restart,
		Health:   s.Health,
		Public:   s.Public,
	}

	sort.Strings(normalized.EnvFiles)
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Config) IngressRules(keys []string) ([]IngressRule, error) {
	serviceKeys, err := c.ServiceKeys(keys)
	if err != nil {
		return nil, err
	}

	rules := make([]IngressRule, 0, len(serviceKeys)+1)
	for _, key := range serviceKeys {
		service := c.Services[key]
		if strings.TrimSpace(service.Public.Hostname) == "" {
			continue
		}
		rules = append(rules, IngressRule{
			Hostname: service.Public.Hostname,
			Service:  fmt.Sprintf("http://localhost:%d", service.Port),
		})
	}
	rules = append(rules, IngressRule{Service: "http_status:404"})
	return rules, nil
}

type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}
