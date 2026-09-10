package spawn

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"erbrus/internal/tools"
)

// Driver is everything the server needs from the platform: the
// multiplexer (Spawner + extras) and the host shell. Nothing outside the
// spawn package names tmux, WezTerm, sh or PowerShell (spec
// docs/superpowers/specs/2026-09-10-wezterm-spawner-design.md).
type Driver interface {
	Spawner
	// Name is stored in run.Spawner: "tmux" | "wezterm".
	Name() string
	// WriteCommand writes the file `erbrus wrap` executes for a run:
	// cmd.sh (exec …) on posix, cmd.json (argv, no shell) on Windows.
	WriteCommand(runDir, command string) (cmdFile string, err error)
	// PromptByPaste: the prompt never goes on the command line (Windows:
	// cmd.exe shims read one line, PowerShell 5.1 mangles quotes).
	PromptByPaste() bool
	// AgentBin is the erbrus path as agents should type it (forward
	// slashes on Windows: Claude Code runs commands through Git Bash).
	AgentBin(bin string) string
	// ShellTool is the built-in `shell` tool's command template.
	ShellTool() string
	// Viewer reports browser-terminal support (tmux only).
	Viewer() (Viewer, bool)
	// Sweep kills stale browser-terminal sessions at startup.
	Sweep() []string
	// OpenTerminal says how a terminal tool opens in dir: ok=false → a
	// tool run (tmux window + browser terminal); ok=true → launch argv
	// detached (a visible WezTerm window).
	OpenTerminal(dir string, argv []string) (launch []string, ok bool)
	// AttachCommand is the shell command a human types to attach to
	// session from outside erbrus: tmux attach for *Tmux (and any other
	// spawner, including test fakes — DriverFor keeps tmux semantics),
	// `wezterm cli connect` for *Wezterm.
	AttachCommand(session string) string
}

// StdinRunner is CmdRunner with stdin, for `wezterm cli send-text`.
type StdinRunner func(stdin, name string, args ...string) ([]byte, error)

func ExecStdinRunner(stdin, name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Stdin = strings.NewReader(stdin)
	return c.Output()
}

// NewDriver builds the platform driver: the spawner from terminal ("" =
// tmux on posix, wezterm on windows), the host from goos.
func NewDriver(terminal, weztermBin, goos string, run CmdRunner, in StdinRunner) (Driver, error) {
	var h host = posixHost{}
	if goos == "windows" {
		h = windowsHost{}
	}
	if terminal == "" {
		terminal = "tmux"
		if goos == "windows" {
			terminal = "wezterm"
		}
	}
	switch terminal {
	case "tmux":
		return &driver{Spawner: NewTmux(run), host: h, name: "tmux"}, nil
	case "wezterm":
		if weztermBin == "" {
			weztermBin = "wezterm"
		}
		return &driver{Spawner: NewWezterm(in, weztermBin), host: h, name: "wezterm"}, nil
	}
	return nil, fmt.Errorf("config terminal %q: want tmux or wezterm", terminal)
}

// DriverFor wraps a bare Spawner (tests) with posix host defaults.
func DriverFor(sp Spawner) Driver {
	return &driver{Spawner: sp, host: posixHost{}, name: "tmux"}
}

type host interface {
	WriteCommand(runDir, command string) (string, error)
	PromptByPaste() bool
	AgentBin(bin string) string
	ShellTool() string
}

type driver struct {
	Spawner
	host
	name string
}

func (d *driver) Name() string { return d.name }

// SendChecked forwards to the wrapped spawner's own SendChecked (tmux,
// WezTerm) when it has one, else falls back to plain Send. Without this
// forwarding method, embedding Spawner as an interface field only
// promotes the Spawner interface's own methods, so a checkedSender type
// assertion on the driver would always miss the underlying spawner's
// extra method — silently losing the provider's own "submitted" rules.
func (d *driver) SendChecked(h Handle, text string, submitted func(string) bool) error {
	if cs, ok := d.Spawner.(interface {
		SendChecked(Handle, string, func(string) bool) error
	}); ok {
		return cs.SendChecked(h, text, submitted)
	}
	return d.Spawner.Send(h, text)
}

func (d *driver) Viewer() (Viewer, bool) {
	v, ok := d.Spawner.(Viewer)
	return v, ok
}

func (d *driver) Sweep() []string {
	if t, ok := d.Spawner.(*Tmux); ok {
		return t.SweepViews()
	}
	return nil
}

func (d *driver) OpenTerminal(dir string, argv []string) ([]string, bool) {
	if w, ok := d.Spawner.(*Wezterm); ok {
		return w.OpenTerminal(dir, argv)
	}
	return nil, false
}

// AttachCommand: *Wezterm connects to the mux server's named workspace;
// everything else (*Tmux, and test fakes via DriverFor) keeps tmux
// semantics.
func (d *driver) AttachCommand(session string) string {
	if w, ok := d.Spawner.(*Wezterm); ok {
		return w.bin + " connect unix --workspace " + session
	}
	return "tmux attach -t " + session
}

// posixHost: sh runs cmd.sh; the prompt may sit on the command line.
type posixHost struct{}

func (posixHost) WriteCommand(runDir, command string) (string, error) {
	p := filepath.Join(runDir, "cmd.sh")
	return p, os.WriteFile(p, []byte("#!/bin/sh\nexec "+command+"\n"), 0o755)
}
func (posixHost) PromptByPaste() bool        { return false }
func (posixHost) AgentBin(bin string) string { return bin }
func (posixHost) ShellTool() string          { return "${SHELL:-bash}" }

// windowsHost: no shell at all. The rendered command (POSIX-quoted) is
// split into argv and executed directly by `erbrus wrap`.
type windowsHost struct{}

func (windowsHost) WriteCommand(runDir, command string) (string, error) {
	argv, err := tools.Split(command)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(argv)
	if err != nil {
		return "", err
	}
	p := filepath.Join(runDir, "cmd.json")
	return p, os.WriteFile(p, append(b, '\n'), 0o644)
}
func (windowsHost) PromptByPaste() bool        { return true }
func (windowsHost) AgentBin(bin string) string { return strings.ReplaceAll(bin, `\`, "/") }
func (windowsHost) ShellTool() string          { return "powershell" }
