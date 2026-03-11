package logger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lmittmann/tint"
	"golang.org/x/term"
	"gopkg.in/natefinch/lumberjack.v2"
)

var loggers sync.Map

func New(name string) *slog.Logger {
	if existing, ok := loggers.Load(name); ok {
		return existing.(*slog.Logger)
	}

	level := parseLevel(os.Getenv("LOG_LEVEL"))
	console := tint.NewHandler(os.Stderr, &tint.Options{
		Level:      level,
		NoColor:    !term.IsTerminal(int(os.Stderr.Fd())),
		TimeFormat: "15:04:05",
	})

	logDir := filepath.Join(os.Getenv("HOME"), ".local", "log")
	_ = os.MkdirAll(logDir, 0o755)
	file := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, name+".jsonl"),
		MaxSize:    5,
		MaxBackups: 3,
	}
	jsonHandler := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level})

	instance := slog.New(&multiHandler{handlers: []slog.Handler{console, jsonHandler}}).With("logger", name)
	actual, _ := loggers.LoadOrStore(name, instance)
	return actual.(*slog.Logger)
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range m.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range m.handlers {
		if handler.Enabled(ctx, record.Level) {
			if err := handler.Handle(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for index, handler := range m.handlers {
		handlers[index] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for index, handler := range m.handlers {
		handlers[index] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}
