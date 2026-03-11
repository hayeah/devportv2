package devport

import (
	"regexp"
	"testing"
)

func TestTmuxWindowNameUsesStableHash(t *testing.T) {
	t.Parallel()

	tmux := NewTmux("test")
	window := tmux.WindowName("app/web")

	if window != tmux.WindowName("app/web") {
		t.Fatalf("expected stable window name, got %q", window)
	}
	if window == tmux.WindowName("app/api") {
		t.Fatalf("expected different keys to hash differently")
	}
	if matched := regexp.MustCompile(`^svc-[a-z2-7]{16}$`).MatchString(window); !matched {
		t.Fatalf("unexpected window name format: %q", window)
	}
}
