package spawn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Wezterm drives WezTerm's headless mux server through `wezterm cli`:
// the Windows multiplexer (spec 2026-09-10-wezterm-spawner-design.md).
// A Handle is "<pane id>@<tab title>": pane ids restart at 0 when the mux
// server restarts, so every lookup matches both.
type Wezterm struct {
	run StdinRunner
	bin string
}

func NewWezterm(run StdinRunner, bin string) *Wezterm { return &Wezterm{run: run, bin: bin} }

func (w *Wezterm) cli(stdin string, args ...string) ([]byte, error) {
	out, err := w.run(stdin, w.bin, append([]string{"cli"}, args...)...)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%w: %s", err, lastLine(string(ee.Stderr)))
		}
		return out, err
	}
	return out, nil
}

// lastLine is the mux's real message: auto-start WARN lines come first.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func splitHandle(h Handle) (id, title string) {
	id, title, _ = strings.Cut(string(h), "@")
	return id, title
}

func (w *Wezterm) Spawn(spec RunSpec) (Handle, error) {
	workspace := spec.Session
	if spec.AttachSession != "" {
		workspace = spec.AttachSession
	}
	args := []string{"spawn", "--new-window", "--workspace", workspace, "--cwd", spec.Workdir, "--"}
	args = append(args, spec.Command...)
	out, err := w.cli("", args...)
	if err != nil {
		return "", fmt.Errorf("wezterm spawn: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if _, err := strconv.Atoi(id); err != nil {
		return "", fmt.Errorf("wezterm spawn: unexpected output %q", out)
	}
	h := Handle(id + "@" + spec.WindowName)
	// The title is half of the run's identity: Alive/Capture require
	// tab_title == title, so a pane whose title never got set reads as
	// gone — Reconcile marks the run failed while the agent keeps
	// running orphaned. Best-effort kill the orphan pane and surface the
	// error instead of pretending this was cosmetic.
	if _, err := w.cli("", "set-tab-title", "--pane-id", id, spec.WindowName); err != nil {
		w.cli("", "kill-pane", "--pane-id", id) // best effort
		return "", fmt.Errorf("wezterm set-tab-title: %w", err)
	}
	return h, nil
}

type wtPane struct {
	PaneID   int    `json:"pane_id"`
	TabTitle string `json:"tab_title"`
	Size     struct {
		Rows int `json:"rows"`
		Cols int `json:"cols"`
	} `json:"size"`
}

// pane finds h in `list`; found=false when the id+title pair is gone.
func (w *Wezterm) pane(h Handle) (p wtPane, found bool, err error) {
	out, err := w.cli("", "list", "--format", "json")
	if err != nil {
		return p, false, fmt.Errorf("wezterm list: %w", err)
	}
	var panes []wtPane
	if err := json.Unmarshal(out, &panes); err != nil {
		return p, false, fmt.Errorf("wezterm list: %w", err)
	}
	id, title := splitHandle(h)
	for _, p := range panes {
		if strconv.Itoa(p.PaneID) == id && p.TabTitle == title {
			return p, true, nil
		}
	}
	return p, false, nil
}

func (w *Wezterm) Alive(h Handle) (bool, error) {
	_, ok, err := w.pane(h)
	return ok, err
}

func (w *Wezterm) Capture(h Handle) (Screen, error) {
	p, ok, err := w.pane(h)
	if err != nil {
		return Screen{}, err
	}
	if !ok {
		return Screen{}, fmt.Errorf("capture %s: pane gone", h)
	}
	id, _ := splitHandle(h)
	out, err := w.cli("", "get-text", "--escapes", "--pane-id", id)
	if err != nil {
		return Screen{}, fmt.Errorf("capture %s: %w", h, err)
	}
	return Screen{Raw: string(out), Cols: p.Size.Cols, Rows: p.Size.Rows}, nil
}

func (w *Wezterm) Send(h Handle, text string) error { return w.SendChecked(h, text, nil) }

func (w *Wezterm) SendChecked(h Handle, text string, submitted func(string) bool) error {
	id, _ := splitHandle(h)
	paste := func() error {
		if _, err := w.cli(text, "send-text", "--pane-id", id); err != nil {
			return fmt.Errorf("send to %s: %w", h, err)
		}
		return nil
	}
	enter := func() error {
		if _, err := w.cli("\r", "send-text", "--no-paste", "--pane-id", id); err != nil {
			return fmt.Errorf("send Enter to %s: %w", h, err)
		}
		return nil
	}
	capture := func() (string, error) {
		out, err := w.cli("", "get-text", "--pane-id", id)
		return string(out), err
	}
	return sendChecked(string(h), text, submitted, paste, enter, capture)
}

// keyBytes maps the keypad's tmux key names to what a terminal sends.
var keyBytes = map[string]string{"Enter": "\r", "Escape": "\x1b", "Up": "\x1b[A", "Down": "\x1b[B", "Tab": "\t"}

// SendKeys checks the id+title pair before typing: after a mux-server
// restart pane ids restart at 0, so a stale run row could otherwise send
// keystrokes into an unrelated pane. (Send already goes through Capture
// in the server, so it gets this check for free.)
func (w *Wezterm) SendKeys(h Handle, key string) error {
	b, ok := keyBytes[key]
	if !ok {
		if len([]rune(key)) != 1 {
			return fmt.Errorf("key %q not supported by the wezterm spawner", key)
		}
		b = key
	}
	if _, found, err := w.pane(h); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("send key %q to %s: pane gone", key, h)
	}
	id, _ := splitHandle(h)
	if _, err := w.cli(b, "send-text", "--no-paste", "--pane-id", id); err != nil {
		return fmt.Errorf("send key %q to %s: %w", key, h, err)
	}
	return nil
}

// Stop checks the id+title pair before killing: see SendKeys.
func (w *Wezterm) Stop(h Handle) error {
	if _, found, err := w.pane(h); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("stop %s: pane gone", h)
	}
	id, _ := splitHandle(h)
	if _, err := w.cli("", "kill-pane", "--pane-id", id); err != nil {
		return fmt.Errorf("kill %s: %w", h, err)
	}
	return nil
}

// OpenTerminal: a terminal tool becomes a visible WezTerm window in dir.
func (w *Wezterm) OpenTerminal(dir string, argv []string) ([]string, bool) {
	return append([]string{w.bin, "start", "--cwd", dir, "--"}, argv...), true
}
