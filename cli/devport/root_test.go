package main

import "testing"

func TestCommandConstructorsAndSingleKey(t *testing.T) {
	options := &rootOptions{}
	commands := []*struct {
		name string
		cmd  func(*rootOptions) any
	}{
		{name: "up", cmd: func(options *rootOptions) any { return newUpCommand(options) }},
		{name: "down", cmd: func(options *rootOptions) any { return newDownCommand(options) }},
		{name: "start", cmd: func(options *rootOptions) any { return newStartCommand(options) }},
		{name: "stop", cmd: func(options *rootOptions) any { return newStopCommand(options) }},
		{name: "restart", cmd: func(options *rootOptions) any { return newRestartCommand(options) }},
		{name: "status", cmd: func(options *rootOptions) any { return newStatusCommand(options) }},
		{name: "logs", cmd: func(options *rootOptions) any { return newLogsCommand(options) }},
		{name: "freeport", cmd: func(options *rootOptions) any { return newFreePortCommand(options) }},
		{name: "ingress", cmd: func(options *rootOptions) any { return newIngressCommand(options) }},
		{name: "supervise", cmd: func(options *rootOptions) any { return newSuperviseCommand(options) }},
	}
	for _, command := range commands {
		if command.cmd(options) == nil {
			t.Fatalf("%s constructor returned nil", command.name)
		}
	}

	if _, err := singleKey(nil); err == nil {
		t.Fatalf("expected singleKey(nil) to fail")
	}
	if _, err := singleKey([]string{"a", "b"}); err == nil {
		t.Fatalf("expected singleKey(multiple) to fail")
	}
	key, err := singleKey([]string{"app/web"})
	if err != nil {
		t.Fatalf("singleKey(valid): %v", err)
	}
	if key != "app/web" {
		t.Fatalf("unexpected key: %s", key)
	}
}
