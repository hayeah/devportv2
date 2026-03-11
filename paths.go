package devport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Config string
	State  string
	DB     string
	Locks  string
}

func ResolvePaths(explicitConfig string) (Paths, error) {
	configPath, err := resolveConfigPath(explicitConfig)
	if err != nil {
		return Paths{}, err
	}

	stateDir, err := resolveStateDir()
	if err != nil {
		return Paths{}, err
	}

	return Paths{
		Config: configPath,
		State:  stateDir,
		DB:     filepath.Join(stateDir, "devport.db"),
		Locks:  filepath.Join(stateDir, "locks"),
	}, nil
}

func resolveConfigPath(explicit string) (string, error) {
	switch {
	case explicit != "":
		return ExpandPath(explicit)
	case os.Getenv("DEVPORT_CONFIG") != "":
		return ExpandPath(os.Getenv("DEVPORT_CONFIG"))
	default:
		return ExpandPath("~/.config/devport/devport.toml")
	}
}

func resolveStateDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("DEVPORT_STATE_DIR")); value != "" {
		return ExpandPath(value)
	}
	return ExpandPath("~/.local/share/devport")
}

func ExpandPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Abs(os.ExpandEnv(path))
}
