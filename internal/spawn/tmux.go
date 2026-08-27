package spawn

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type Tmux struct{ run CmdRunner }

func NewTmux(run CmdRunner) *Tmux { return &Tmux{run: run} }

func (t *Tmux) Spawn(spec RunSpec) (Handle, error) {
	session := spec.Session
	if spec.AttachSession != "" {
		session = spec.AttachSession
		if _, err := t.run("tmux", "has-session", "-t", session); err != nil {
			return "", fmt.Errorf("attach_session %q not found: %w", session, err)
		}
	} else {
		if _, err := t.run("tmux", "has-session", "-t", session); err != nil {
			if _, err := t.run("tmux", "new-session", "-d", "-s", session, "-c", spec.Workdir); err != nil {
				return "", fmt.Errorf("create session %q: %w", session, err)
			}
		}
	}

	args := []string{"new-window", "-d", "-P", "-F", "#{session_name}:#{window_index}",
		"-t", session + ":", "-n", spec.WindowName, "-c", spec.Workdir}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, "--")
	args = append(args, spec.Command...)
	out, err := t.run("tmux", args...)
	if err != nil {
		return "", fmt.Errorf("open window: %w", err)
	}
	h := Handle(strings.TrimSpace(string(out)))
	if _, err := t.run("tmux", "set-option", "-t", string(h), "remain-on-exit", "on"); err != nil {
		return h, nil // cosmetic option; the run is already up
	}
	return h, nil
}

// sendSettle is how long Send waits between the paste and the Enter.
// Agent TUIs (Claude Code) swallow an Enter that arrives within ~1ms of
// the bracketed-paste end, leaving the text sitting unsubmitted in the
// input box — observed live 2026-08-28.
var sendSettle = 300 * time.Millisecond

// Send delivers text into the window as a bracketed paste (-p), then
// presses Enter. Paste, not send-keys -l: interactive agents submit on
// newline, so a multiline context block would otherwise fire line by line.
func (t *Tmux) Send(h Handle, text string) error {
	if _, err := t.run("tmux", "set-buffer", "--", text); err != nil {
		return fmt.Errorf("send to %s: %w", h, err)
	}
	if _, err := t.run("tmux", "paste-buffer", "-dp", "-t", string(h)); err != nil {
		return fmt.Errorf("send to %s: %w", h, err)
	}
	time.Sleep(sendSettle)
	if _, err := t.run("tmux", "send-keys", "-t", string(h), "Enter"); err != nil {
		return fmt.Errorf("send Enter to %s: %w", h, err)
	}
	return nil
}

func (t *Tmux) Stop(h Handle) error {
	_, err := t.run("tmux", "kill-window", "-t", string(h))
	return err
}

func (t *Tmux) Alive(h Handle) (bool, error) {
	session, _, ok := strings.Cut(string(h), ":")
	if !ok {
		return false, fmt.Errorf("malformed handle %q", h)
	}
	out, err := t.run("tmux", "list-windows", "-t", session, "-F", "#{session_name}:#{window_index}")
	if err != nil {
		return false, nil // session gone => not alive, not an error
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == string(h) {
			return true, nil
		}
	}
	return false, nil
}
