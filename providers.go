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

type Runtime struct {
	Config RuntimeConfig
	IO     ManagerIO
	Paths  Paths
	Spec   *Config
}

func ProvideRuntime(config RuntimeConfig, managerIO ManagerIO) (*Runtime, error) {
	paths, err := ResolvePathsWithRuntime(config)
	if err != nil {
		return nil, err
	}
	spec, err := LoadConfig(paths.Config)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Config: config,
		IO:     managerIO,
		Paths:  paths,
		Spec:   spec,
	}, nil
}

func ProvideStore(runtime *Runtime) (*Store, error) {
	return OpenStore(runtime.Paths.DB)
}

func ProvideExecutable() (string, error) {
	return os.Executable()
}

func ProvideTmux(runtime *Runtime) *Tmux {
	return NewTmux(runtime.Spec.TmuxSession)
}

func ProvideManagerLogger() *slog.Logger {
	return logger.New("devport")
}

func NewManagerFromDeps(
	runtime *Runtime,
	store *Store,
	tmux *Tmux,
	log *slog.Logger,
	executable string,
) *Manager {
	return &Manager{
		runtime:    runtime,
		store:      store,
		tmux:       tmux,
		log:        log,
		executable: executable,
	}
}
