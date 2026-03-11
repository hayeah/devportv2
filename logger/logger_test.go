package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"WARN":    slog.LevelWarn,
		"error":   slog.LevelError,
		"unknown": slog.LevelInfo,
	}
	for input, expected := range tests {
		if actual := parseLevel(input); actual != expected {
			t.Fatalf("parseLevel(%q) = %v, want %v", input, actual, expected)
		}
	}
}

func TestMultiHandlerEnabled(t *testing.T) {
	handler := &multiHandler{handlers: []slog.Handler{
		slog.NewTextHandler(testWriter{}, &slog.HandlerOptions{Level: slog.LevelInfo}),
	}}
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatalf("expected info level to be enabled")
	}
	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatalf("expected debug level to be disabled")
	}
}

type testWriter struct{}

func (testWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
