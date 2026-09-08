// Package spawn launches agent runs into terminal multiplexers. TmuxSpawner
// is the v1 implementation; zellij/headless are future Spawners. erbrus
// only ever kills windows it created.
package spawn

import (
	"os/exec"
	"time"
)

type CmdRunner func(name string, args ...string) ([]byte, error)

func ExecCmdRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type RunSpec struct {
	Session       string
	AttachSession string
	WindowName    string
	Workdir       string
	Env           map[string]string
	Command       []string
}

type Handle string

// Screen is one snapshot of a run's terminal: the rendered pane with SGR
// escapes (what a human attached to tmux would see) plus the tmux
// bookkeeping the UI needs to say "dead" or "quiet for 40s".
type Screen struct {
	Raw      string    // capture-pane -p -e output, one line per row
	Dead     bool      // pane process has exited (remain-on-exit keeps the screen)
	Activity time.Time // tmux window_activity; zero when unknown
	Cols     int
	Rows     int
}

type Spawner interface {
	Spawn(spec RunSpec) (Handle, error)
	Stop(h Handle) error
	Alive(h Handle) (bool, error)
	// Send types text into the run's terminal, followed by Enter — the way
	// a human would talk to the interactive agent sitting in that window.
	Send(h Handle, text string) error
	// Capture snapshots the run's visible screen. Errors when the window
	// is gone; a dead-but-retained pane still captures (Dead=true).
	Capture(h Handle) (Screen, error)
	// SendKeys presses one key (tmux key name: "Enter", "Escape", "Down",
	// or a single character) — how a human answers a dialog.
	SendKeys(h Handle, key string) error
}
