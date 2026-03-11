package devport

import (
	"io"
	"log/slog"
	"os"

	"github.com/hayeah/devportv2/logger"
)

type ManagerIO struct {
	Stdout io.Writer
	Stderr io.Writer
}

func ProvidePaths(runtime RuntimeConfig) (Paths, error) {
	return ResolvePathsWithRuntime(runtime)
}

func ProvideConfig(paths Paths) (*Config, error) {
	return LoadConfig(paths.Config)
}

func ProvideStore(paths Paths) (*Store, error) {
	return OpenStore(paths.DB)
}

func ProvideExecutable() (string, error) {
	return os.Executable()
}

func ProvideTmux(config *Config) *Tmux {
	return NewTmux(config.TmuxSession)
}

func ProvideManagerLogger() *slog.Logger {
	return logger.New("devport")
}

func NewManagerFromDeps(
	runtime RuntimeConfig,
	managerIO ManagerIO,
	paths Paths,
	config *Config,
	store *Store,
	tmux *Tmux,
	log *slog.Logger,
	executable string,
) *Manager {
	return &Manager{
		runtime:    runtime,
		paths:      paths,
		config:     config,
		store:      store,
		tmux:       tmux,
		log:        log,
		stdout:     managerIO.Stdout,
		stderr:     managerIO.Stderr,
		executable: executable,
	}
}
