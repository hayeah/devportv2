package devport

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Tmux struct {
	session string
}

func NewTmux(session string) *Tmux {
	return &Tmux{session: session}
}

func (t *Tmux) WindowName(key string) string {
	sum := sha256.Sum256([]byte(key))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return "svc-" + strings.ToLower(encoded[:16])
}

func (t *Tmux) Target(window string) string {
	return t.session + ":" + window
}

func (t *Tmux) Start(window string, env map[string]string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("tmux command is empty")
	}
	envArgs := tmuxEnvironmentArgs(env)
	if t.SessionExists() {
		if t.WindowExists(window) {
			if err := t.KillWindow(window); err != nil {
				return err
			}
		}
		args := append([]string{"new-window", "-d", "-t", t.session, "-n", window}, envArgs...)
		args = append(args, command...)
		if err := exec.Command("tmux", args...).Run(); err != nil {
			return fmt.Errorf("tmux new-window: %w", err)
		}
		return t.setRemainOnExit(window)
	}

	args := append([]string{"new-session", "-d", "-s", t.session, "-n", window}, envArgs...)
	args = append(args, command...)
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	return t.setRemainOnExit(window)
}

func (t *Tmux) SessionExists() bool {
	return exec.Command("tmux", "has-session", "-t", t.session).Run() == nil
}

func (t *Tmux) WindowExists(window string) bool {
	return exec.Command("tmux", "list-windows", "-t", t.session, "-F", "#{window_name}").Run() == nil &&
		strings.Contains(t.windowNames(), "\n"+window+"\n")
}

func (t *Tmux) KillWindow(window string) error {
	if !t.WindowExists(window) {
		return nil
	}
	if err := exec.Command("tmux", "kill-window", "-t", t.Target(window)).Run(); err != nil {
		return fmt.Errorf("tmux kill-window: %w", err)
	}
	return nil
}

func (t *Tmux) CapturePane(window string, lines int) (string, error) {
	start := fmt.Sprintf("-%d", lines)
	output, err := exec.Command("tmux", "capture-pane", "-p", "-J", "-S", start, "-t", t.Target(window)).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (t *Tmux) AttachWindow(window string) error {
	if !t.SessionExists() {
		return fmt.Errorf("tmux session %q not found", t.session)
	}
	if !t.WindowExists(window) {
		return fmt.Errorf("tmux window %q not found", window)
	}
	if err := exec.Command("tmux", "select-window", "-t", t.Target(window)).Run(); err != nil {
		return fmt.Errorf("tmux select-window: %w", err)
	}
	if os.Getenv("TMUX") != "" {
		if err := exec.Command("tmux", "switch-client", "-t", t.session).Run(); err != nil {
			return fmt.Errorf("tmux switch-client: %w", err)
		}
		return nil
	}

	command := exec.Command("tmux", "attach-session", "-t", t.session)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("tmux attach-session: %w", err)
	}
	return nil
}

func (t *Tmux) setRemainOnExit(window string) error {
	if err := exec.Command("tmux", "set-option", "-t", t.Target(window), "remain-on-exit", "on").Run(); err != nil {
		return fmt.Errorf("tmux set remain-on-exit: %w", err)
	}
	return nil
}

func (t *Tmux) windowNames() string {
	output, err := exec.Command("tmux", "list-windows", "-t", t.session, "-F", "#{window_name}").CombinedOutput()
	if err != nil {
		return "\n"
	}
	return "\n" + string(output) + "\n"
}

func tmuxEnvironmentArgs(env map[string]string) []string {
	keys := []string{"HOME", "PATH", "LOG_LEVEL"}
	args := []string{}
	for _, key := range keys {
		value, ok := env[key]
		if !ok {
			continue
		}
		args = append(args, "-e", key+"="+value)
	}
	return args
}
