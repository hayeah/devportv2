package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	devport "github.com/hayeah/devportv2"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	configPath  string
	runtimeJSON string
	keys        []string
}

type ManagerFactory struct {
	managerIO devport.ManagerIO
}

type App struct {
	managerFactory *ManagerFactory
	managerIO      devport.ManagerIO
}

func NewManagerFactory(managerIO devport.ManagerIO) *ManagerFactory {
	return &ManagerFactory{managerIO: managerIO}
}

func NewApp(managerFactory *ManagerFactory, managerIO devport.ManagerIO) *App {
	return &App{managerFactory: managerFactory, managerIO: managerIO}
}

func Execute(managerIO devport.ManagerIO) error {
	app := InitializeApp(managerIO)
	return app.RootCommand().Execute()
}

func (a *App) RootCommand() *cobra.Command {
	options := &rootOptions{}
	root := &cobra.Command{
		Use:           "devport",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&options.configPath, "file", "", "path to devport.toml")
	root.PersistentFlags().StringVar(&options.runtimeJSON, "runtime-json", "", "internal runtime overrides")
	_ = root.PersistentFlags().MarkHidden("runtime-json")

	root.AddCommand(a.newUpCommand(options))
	root.AddCommand(a.newDownCommand(options))
	root.AddCommand(a.newStartCommand(options))
	root.AddCommand(a.newStopCommand(options))
	root.AddCommand(a.newRestartCommand(options))
	root.AddCommand(a.newStatusCommand(options))
	root.AddCommand(a.newLogsCommand(options))
	root.AddCommand(a.newFreePortCommand(options))
	root.AddCommand(a.newIngressCommand(options))
	root.AddCommand(a.newSuperviseCommand(options))
	return root
}

func (a *App) newUpCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "up",
		Short: "Start services from the spec",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			return manager.Up(context.Background(), options.keys)
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newDownCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "down",
		Short: "Stop services from the spec",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			return manager.Down(context.Background(), options.keys)
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newStartCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "start",
		Short: "Start one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			return manager.Start(context.Background(), key, "start")
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newStopCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "stop",
		Short: "Stop one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			return manager.Stop(context.Background(), key, "stop")
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newRestartCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "restart",
		Short: "Restart one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			return manager.Restart(context.Background(), key)
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newStatusCommand(options *rootOptions) *cobra.Command {
	var jsonOutput bool
	var diffOnly bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Report service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			statuses, err := manager.Status(context.Background(), options.keys)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(a.managerIO.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(statuses)
			}
			return manager.PrintStatus(statuses, diffOnly)
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	command.Flags().BoolVar(&jsonOutput, "json", false, "print JSON")
	command.Flags().BoolVar(&diffOnly, "diff", false, "show drift only")
	return command
}

func (a *App) newLogsCommand(options *rootOptions) *cobra.Command {
	var lines int
	command := &cobra.Command{
		Use:   "logs",
		Short: "Show recent logs for one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			output, err := manager.Logs(context.Background(), key, lines)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(a.managerIO.Stdout, output)
			return err
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	command.Flags().IntVar(&lines, "lines", 200, "number of lines to capture")
	return command
}

func (a *App) newFreePortCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "freeport",
		Short: "Return the next free port in the configured range",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			port, err := manager.FreePort(options.keys)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.managerIO.Stdout, port)
			return err
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newIngressCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "ingress",
		Short: "Export public hostnames as ingress JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			document, err := manager.Ingress(options.keys)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.managerIO.Stdout, string(document))
			return err
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func (a *App) newSuperviseCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:    "supervise",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := a.manager(options)
			if err != nil {
				return err
			}
			defer manager.Close()
			return manager.Supervise(context.Background(), key)
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func singleKey(keys []string) (string, error) {
	if len(keys) != 1 {
		return "", fmt.Errorf("exactly one --key is required")
	}
	return keys[0], nil
}

func (a *App) manager(options *rootOptions) (*devport.Manager, error) {
	runtime, err := options.runtime()
	if err != nil {
		return nil, err
	}
	return a.managerFactory.Manager(runtime)
}

func (factory *ManagerFactory) Manager(runtime devport.RuntimeConfig) (*devport.Manager, error) {
	return devport.InitializeManager(runtime, factory.managerIO)
}

func (options *rootOptions) runtime() (devport.RuntimeConfig, error) {
	runtime := devport.RuntimeConfig{}
	if strings.TrimSpace(options.runtimeJSON) != "" {
		if err := json.Unmarshal([]byte(options.runtimeJSON), &runtime); err != nil {
			return devport.RuntimeConfig{}, fmt.Errorf("parse runtime-json: %w", err)
		}
	}
	if options.configPath != "" {
		runtime.ConfigPath = options.configPath
	}
	return runtime, nil
}
