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
	// Size reports the window's current columns and rows.
	Size(h Handle) (cols, rows int, err error)
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

// Size is the window's size as tmux renders it to every client.
func (t *Tmux) Size(h Handle) (int, int, error) {
	out, err := t.run("tmux", "display", "-p", "-t", exact(string(h)), "#{window_width} #{window_height}")
	if err != nil {
		return 0, 0, fmt.Errorf("size of %s: %w", h, err)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("size of %s: unexpected %q", h, out)
	}
	cols, err1 := strconv.Atoi(f[0])
	rows, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || cols <= 0 || rows <= 0 {
		return 0, 0, fmt.Errorf("size of %s: unexpected %q", h, out)
	}
	return cols, rows, nil
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
