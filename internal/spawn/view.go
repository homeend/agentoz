package spawn

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Viewer is implemented by spawners whose windows can be attached to
// interactively. The server type-asserts it; other spawners keep the
// read-only screen page.
type Viewer interface {
	// ViewCommand returns the argv of a client that shows h at the
	// window's own size without affecting other clients, in a session
	// named view. internal/termview runs it in a pty.
	ViewCommand(h Handle, view string) ([]string, error)
	// Size reports the window's current columns and rows; an error when
	// the window is gone (never a fallback to another window).
	Size(h Handle) (cols, rows int, err error)
	// GuardView arranges for the view session to end as soon as the
	// window it shows disappears, so it can never show another one.
	GuardView(view string) error
}

// viewPrefix names the sessions erbrus creates for browser terminals.
// SweepViews relies on it; never use the prefix for anything else.
const viewPrefix = "erbrus-view-"

// ViewName returns a session name unique per browser tab, so two tabs on
// one run get independent grouped sessions.
func ViewName(runID int64) string {
	var b [4]byte
	rand.Read(b[:])
	return fmt.Sprintf("%s%d-%s", viewPrefix, runID, hex.EncodeToString(b[:]))
}

// ViewCommand builds a grouped session (-t <session>: shares the windows,
// has its own current window), size-neutral (-f ignore-size: the user's
// `window-size latest` server would otherwise reflow their terminal to
// the browser's size — seen in probes 2026-09-08), self-destroying when
// the pty client goes, without status line or prefix key so a browser
// tab cannot issue tmux commands, showing exactly h's window.
func (t *Tmux) ViewCommand(h Handle, view string) ([]string, error) {
	session, index, ok := strings.Cut(string(h), ":")
	if !ok || session == "" || index == "" {
		return nil, fmt.Errorf("malformed handle %q", h)
	}
	argv := []string{"tmux", "new-session", "-t", session, "-s", view, "-f", "ignore-size"}
	for _, kv := range [][2]string{{"status", "off"}, {"destroy-unattached", "on"}, {"prefix", "None"}, {"prefix2", "None"}} {
		argv = append(argv, ";", "set", "-t", view, kv[0], kv[1])
	}
	argv = append(argv, ";", "select-window", "-t", exact(view+":"+index))
	return argv, nil
}

// Size is the window's size as tmux renders it to every client. It goes
// through list-windows and matches the index itself: `display -t
// =session:N` for a missing N silently answers for the session's CURRENT
// window, which let a browser terminal survive its window's death and
// land on the user's own shell (seen live 2026-09-08).
func (t *Tmux) Size(h Handle) (int, int, error) {
	session, index, ok := strings.Cut(string(h), ":")
	if !ok {
		return 0, 0, fmt.Errorf("malformed handle %q", h)
	}
	out, err := t.run("tmux", "list-windows", "-t", exact(session), "-F", "#{window_index} #{window_width} #{window_height}")
	if err != nil {
		return 0, 0, fmt.Errorf("size of %s: %w", h, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != index {
			continue
		}
		cols, err1 := strconv.Atoi(f[1])
		rows, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || cols <= 0 || rows <= 0 {
			return 0, 0, fmt.Errorf("size of %s: unexpected %q", h, line)
		}
		return cols, rows, nil
	}
	return 0, 0, fmt.Errorf("size of %s: window gone", h)
}

// GuardView makes the view session die the moment its window does. A
// grouped session whose current window is killed switches to another
// window of the group — the user's own shell — and a browser terminal
// would keep typing there (seen live 2026-09-08). session-window-changed
// fires exactly then and never for other windows (probed on tmux 3.7c).
// It must be installed in a call of its own: set in the creating command
// chain, it would consume that chain's own queued select-window event.
// set-hook takes no "=" prefix; the nonce makes the name exact enough.
func (t *Tmux) GuardView(view string) error {
	_, err := t.run("tmux", "set-hook", "-t", view, "session-window-changed",
		"run-shell 'tmux kill-session -t "+exact(view)+"'")
	if err != nil {
		return fmt.Errorf("guard view %s: %w", view, err)
	}
	return nil
}

// SweepViews kills view sessions nobody is attached to — leftovers of a
// server that died with terminals open (destroy-unattached only fires on
// detach). Attached ones belong to a live handler. No tmux server means
// nothing to sweep.
func (t *Tmux) SweepViews() (killed []string) {
	out, err := t.run("tmux", "list-sessions", "-F", "#{session_name} #{session_attached}")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, attached, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(name, viewPrefix) || attached != "0" {
			continue
		}
		if _, err := t.run("tmux", "kill-session", "-t", exact(name)); err == nil {
			killed = append(killed, name)
		}
	}
	return killed
}
