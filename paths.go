package devport

import (
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
	return ResolvePathsWithRuntime(RuntimeConfig{ConfigPath: explicitConfig})
}

func ResolvePathsWithRuntime(runtime RuntimeConfig) (Paths, error) {
	configPath, err := resolveConfigPath(runtime)
	if err != nil {
		return Paths{}, err
	}

	stateDir, err := resolveStateDir(runtime)
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

func resolveConfigPath(runtime RuntimeConfig) (string, error) {
	switch {
	case strings.TrimSpace(runtime.ConfigPath) != "":
		return runtime.ExpandPath(runtime.ConfigPath)
	case lookupNonEmpty(runtime, "DEVPORT_ROOT") != "":
		root := lookupNonEmpty(runtime, "DEVPORT_ROOT")
		return runtime.ExpandPath(filepath.Join(root, "devport.toml"))
	default:
		return runtime.ExpandPath("~/.config/devport/devport.toml")
	}
}

func resolveStateDir(runtime RuntimeConfig) (string, error) {
	if strings.TrimSpace(runtime.RootDir) != "" {
		return runtime.ExpandPath(runtime.RootDir)
	}
	if value := lookupNonEmpty(runtime, "DEVPORT_ROOT"); value != "" {
		return runtime.ExpandPath(value)
	}
	return runtime.ExpandPath("~/.local/share/devport")
}

func ExpandPath(path string) (string, error) {
	return RuntimeConfig{}.ExpandPath(path)
}

func lookupNonEmpty(runtime RuntimeConfig, key string) string {
	value, ok := runtime.LookupEnv(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
