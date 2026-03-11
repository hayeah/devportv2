package main

import (
	"io"
	"testing"

	devport "github.com/hayeah/devportv2"
)

func TestInitializeAppAndSingleKey(t *testing.T) {
	app := InitializeApp(devport.ManagerIO{Stdout: io.Discard, Stderr: io.Discard})
	if app == nil {
		t.Fatalf("expected app")
	}
	if app.RootCommand() == nil {
		t.Fatalf("expected root command")
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
