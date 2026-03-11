package devport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RuntimeConfig struct {
	ConfigPath string            `json:"config_path,omitempty"`
	RootDir    string            `json:"root_dir,omitempty"`
	HomeDir    string            `json:"home_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

func (r RuntimeConfig) LookupEnv(key string) (string, bool) {
	if value, ok := r.Env[key]; ok {
		return value, true
	}
	return os.LookupEnv(key)
}

func (r RuntimeConfig) Home() (string, error) {
	if value := strings.TrimSpace(r.HomeDir); value != "" {
		return value, nil
	}
	if value, ok := r.LookupEnv("HOME"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return home, nil
}

func (r RuntimeConfig) ExpandPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(path, "~") {
		home, err := r.Home()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, path[2:])
		}
	}
	expanded := os.Expand(path, func(key string) string {
		value, _ := r.LookupEnv(key)
		return value
	})
	return filepath.Abs(expanded)
}

func (r RuntimeConfig) MarshalJSONValue() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r RuntimeConfig) TmuxEnvironment() map[string]string {
	env := map[string]string{}
	if home, err := r.Home(); err == nil && home != "" {
		env["HOME"] = home
	}
	for _, key := range []string{"PATH", "LOG_LEVEL"} {
		if value, ok := r.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return env
}
