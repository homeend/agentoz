package spawn

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Tmux struct{ run CmdRunner }

func NewTmux(run CmdRunner) *Tmux { return &Tmux{run: run} }

// exact prefixes a tmux target with "=" to force exact-name matching.
// Without it tmux falls back to PREFIX matching: with no session named
// "erbrus", `-t erbrus` silently resolves to "erbrus-gigagit" — observed
// live 2026-08-28, spawning a global-session agent into a project session.
func exact(target string) string { return "=" + target }

func (t *Tmux) Spawn(spec RunSpec) (Handle, error) {
	session := spec.Session
	if spec.AttachSession != "" {
		session = spec.AttachSession
		if _, err := t.run("tmux", "has-session", "-t", exact(session)); err != nil {
			return "", fmt.Errorf("attach_session %q not found: %w", session, err)
		}
	} else {
		if _, err := t.run("tmux", "has-session", "-t", exact(session)); err != nil {
			if _, err := t.run("tmux", "new-session", "-d", "-s", session, "-c", spec.Workdir); err != nil {
				return "", fmt.Errorf("create session %q: %w", session, err)
			}
		}
	}

	args := []string{"new-window", "-d", "-P", "-F", "#{session_name}:#{window_index}",
		"-t", exact(session) + ":", "-n", spec.WindowName, "-c", spec.Workdir}
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
	if _, err := t.run("tmux", "set-option", "-t", exact(string(h)), "remain-on-exit", "on"); err != nil {
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
	if _, err := t.run("tmux", "paste-buffer", "-dp", "-t", exact(string(h))); err != nil {
		return fmt.Errorf("send to %s: %w", h, err)
	}
	time.Sleep(sendSettle)
	if _, err := t.run("tmux", "send-keys", "-t", exact(string(h)), "Enter"); err != nil {
		return fmt.Errorf("send Enter to %s: %w", h, err)
	}
	return nil
}

func (t *Tmux) Stop(h Handle) error {
	_, err := t.run("tmux", "kill-window", "-t", exact(string(h)))
	return err
}

// Alive reports whether the run's pane still hosts a live process. Panes,
// not windows: remain-on-exit keeps the WINDOW around after the agent
// process dies (pane_dead=1), and a window-level check would report such a
// dead agent as alive forever.
func (t *Tmux) Alive(h Handle) (bool, error) {
	session, _, ok := strings.Cut(string(h), ":")
	if !ok {
		return false, fmt.Errorf("malformed handle %q", h)
	}
	out, err := t.run("tmux", "list-panes", "-s", "-t", exact(session), "-F", "#{session_name}:#{window_index} #{pane_dead}")
	if err != nil {
		return false, nil // session gone => not alive, not an error
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		target, dead, ok := strings.Cut(line, " ")
		if ok && target == string(h) {
			return dead == "0", nil
		}
	}
	return false, nil
}

// Capture reads the rendered pane (with color escapes) and the window's
// activity/size/dead flags in two tmux calls, both exact-targeted.
func (t *Tmux) Capture(h Handle) (Screen, error) {
	raw, err := t.run("tmux", "capture-pane", "-p", "-e", "-t", exact(string(h)))
	if err != nil {
		return Screen{}, fmt.Errorf("capture %s: %w", h, err)
	}
	meta, err := t.run("tmux", "display", "-p", "-t", exact(string(h)),
		"#{pane_dead} #{window_activity} #{pane_width} #{pane_height}")
	if err != nil {
		return Screen{}, fmt.Errorf("capture %s: %w", h, err)
	}
	sc := Screen{Raw: string(raw)}
	if f := strings.Fields(string(meta)); len(f) == 4 {
		sc.Dead = f[0] == "1"
		if secs, err := strconv.ParseInt(f[1], 10, 64); err == nil && secs > 0 {
			sc.Activity = time.Unix(secs, 0)
		}
		sc.Cols, _ = strconv.Atoi(f[2])
		sc.Rows, _ = strconv.Atoi(f[3])
	}
	return sc, nil
}
