// Package termview runs a command inside a pseudo-terminal and exposes
// its bytes. The web terminal runs a tmux view client in it (see
// spawn.Viewer); nothing here knows about tmux.
package termview

import (
	"errors"
	"strings"
)

// ErrUnsupported is returned by Start on platforms without a pty.
var ErrUnsupported = errors.New("terminal not supported on this platform")

// Env returns base with TERM and COLORTERM set for a color-capable
// client (tmux renders RGB to the pty only when the client claims it).
func Env(base []string) []string {
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "COLORTERM=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}
