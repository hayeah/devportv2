//go:build wireinject

package main

import (
	"github.com/google/wire"
	devport "github.com/hayeah/devportv2"
)

var AppProviderSet = wire.NewSet(
	NewManagerFactory,
	NewApp,
)

func InitializeApp(managerIO devport.ManagerIO) *App {
	wire.Build(AppProviderSet)
	return nil
}
