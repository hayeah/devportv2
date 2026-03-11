package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	devport "github.com/hayeah/devportv2"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	configPath string
	keys       []string
}

func Execute() {
	options := &rootOptions{}
	root := &cobra.Command{
		Use:           "devport",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&options.configPath, "file", "", "path to devport.toml")

	root.AddCommand(newUpCommand(options))
	root.AddCommand(newDownCommand(options))
	root.AddCommand(newStartCommand(options))
	root.AddCommand(newStopCommand(options))
	root.AddCommand(newRestartCommand(options))
	root.AddCommand(newStatusCommand(options))
	root.AddCommand(newLogsCommand(options))
	root.AddCommand(newFreePortCommand(options))
	root.AddCommand(newIngressCommand(options))
	root.AddCommand(newSuperviseCommand(options))

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newUpCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "up",
		Short: "Start services from the spec",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
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

func newDownCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "down",
		Short: "Stop services from the spec",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
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

func newStartCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "start",
		Short: "Start one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
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

func newStopCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "stop",
		Short: "Stop one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
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

func newRestartCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "restart",
		Short: "Restart one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
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

func newStatusCommand(options *rootOptions) *cobra.Command {
	var jsonOutput bool
	var diffOnly bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Report service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
			if err != nil {
				return err
			}
			defer manager.Close()
			statuses, err := manager.Status(context.Background(), options.keys)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(os.Stdout)
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

func newLogsCommand(options *rootOptions) *cobra.Command {
	var lines int
	command := &cobra.Command{
		Use:   "logs",
		Short: "Show recent logs for one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
			if err != nil {
				return err
			}
			defer manager.Close()
			output, err := manager.Logs(context.Background(), key, lines)
			if err != nil {
				return err
			}
			fmt.Fprint(os.Stdout, output)
			return nil
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	command.Flags().IntVar(&lines, "lines", 200, "number of lines to capture")
	return command
}

func newFreePortCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "freeport",
		Short: "Return the next free port in the configured range",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
			if err != nil {
				return err
			}
			defer manager.Close()
			port, err := manager.FreePort(options.keys)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, port)
			return nil
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func newIngressCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "ingress",
		Short: "Export public hostnames as ingress JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
			if err != nil {
				return err
			}
			defer manager.Close()
			document, err := manager.Ingress(options.keys)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, string(document))
			return nil
		},
	}
	command.Flags().StringArrayVar(&options.keys, "key", nil, "service key")
	return command
}

func newSuperviseCommand(options *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:    "supervise",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := singleKey(options.keys)
			if err != nil {
				return err
			}
			manager, err := devport.NewManager(options.configPath, os.Stdout, os.Stderr)
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
