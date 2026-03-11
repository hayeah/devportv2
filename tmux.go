package devport

import (
	"crypto/sha1"
	"encoding/hex"
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
	sum := sha1.Sum([]byte(key))
	return "svc-" + hex.EncodeToString(sum[:])[:10]
}

func (t *Tmux) Target(window string) string {
	return t.session + ":" + window
}

func (t *Tmux) Start(window string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("tmux command is empty")
	}
	envArgs := tmuxEnvironmentArgs()
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

func tmuxEnvironmentArgs() []string {
	keys := []string{"HOME", "PATH", "LOG_LEVEL", "DEVPORT_STATE_DIR", "DEVPORT_CONFIG"}
	args := []string{}
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		args = append(args, "-e", key+"="+value)
	}
	return args
}
