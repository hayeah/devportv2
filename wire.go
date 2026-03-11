//go:build wireinject

package devport

import "github.com/google/wire"

var ManagerProviderSet = wire.NewSet(
	ProvidePaths,
	ProvideConfig,
	ProvideStore,
	ProvideExecutable,
	ProvideTmux,
	ProvideManagerLogger,
	NewManagerFromDeps,
)

func InitializeManager(runtime RuntimeConfig, managerIO ManagerIO) (*Manager, error) {
	wire.Build(ManagerProviderSet)
	return nil, nil
}
