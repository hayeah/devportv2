package main

import (
	"errors"
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

type attachKeyProviderStub struct {
	keys []string
	err  error
}

func (stub attachKeyProviderStub) ServiceKeys(_ []string) ([]string, error) {
	if stub.err != nil {
		return nil, stub.err
	}
	return stub.keys, nil
}

func TestAttachKey(t *testing.T) {
	t.Parallel()

	provider := attachKeyProviderStub{keys: []string{"app/api", "app/web", "jobs/worker"}}
	chooserCalled := false
	chooser := func(keys []string, query string) (string, error) {
		chooserCalled = true
		if query != "app" {
			t.Fatalf("unexpected query: %q", query)
		}
		if len(keys) != 2 {
			t.Fatalf("unexpected keys: %v", keys)
		}
		return "app/web", nil
	}

	key, err := attachKey(provider, []string{"app/web"}, chooser)
	if err != nil {
		t.Fatalf("attachKey exact: %v", err)
	}
	if key != "app/web" {
		t.Fatalf("unexpected exact key: %s", key)
	}

	key, err = attachKey(provider, []string{"worker"}, chooser)
	if err != nil {
		t.Fatalf("attachKey single match: %v", err)
	}
	if key != "jobs/worker" {
		t.Fatalf("unexpected single match: %s", key)
	}

	key, err = attachKey(provider, []string{"app"}, chooser)
	if err != nil {
		t.Fatalf("attachKey chooser: %v", err)
	}
	if key != "app/web" {
		t.Fatalf("unexpected chooser result: %s", key)
	}
	if !chooserCalled {
		t.Fatalf("expected chooser to be called")
	}

	if _, err := attachKey(provider, []string{"missing"}, chooser); err == nil {
		t.Fatalf("expected no-match error")
	}
	if _, err := attachKey(provider, []string{"a", "b"}, chooser); err == nil {
		t.Fatalf("expected too-many-keys error")
	}

	_, err = attachKey(attachKeyProviderStub{err: errors.New("boom")}, nil, chooser)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected provider error, got %v", err)
	}
}
