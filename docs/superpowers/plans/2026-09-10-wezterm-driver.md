# Platform driver (tmux / WezTerm) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `erbrus.exe serve` on Windows spawns, watches, talks to and stops agent runs through WezTerm, behind one `spawn.Driver` abstraction that also hides tmux and the host shell from the rest of the server.

**Architecture:** `spawn.Driver` = `Spawner` + host methods (`WriteCommand`, `PromptByPaste`, `AgentBin`, `ShellTool`) + mux extras (`Name`, `Viewer`, `Sweep`, `OpenTerminal`). Two hosts (`posixHost`, `windowsHost`) chosen by GOOS; two spawners (`*Tmux`, `*Wezterm`) chosen by config `terminal:`. The server keeps its `spawner` field for the many existing call sites but gains a `driver` used wherever platform knowledge lived (`runs.go`, `serve.go`, `screen.go`, `terminal.go`, `ui_tools.go`, `Reconcile`, the watcher). Runs get an `env` file next to their command file; `erbrus wrap` loads it and runs `.sh` through `sh`, `.json` as a bare argv.

**Tech Stack:** Go 1.27, `wezterm cli` (JSON list, stdin send-text), existing `tools.Split`, recorder-style unit tests.

**Spec:** `docs/superpowers/specs/2026-09-10-wezterm-spawner-design.md`

## Global Constraints

- Never `tmux kill-server`; never touch the user's `erbrus serve` on 7420 or non-`claudetest-*` tmux windows. Throwaway instance = port 7499 with `ERBRUS_URL=http://127.0.0.1:7499` on every CLI call.
- Work in a native worktree (`EnterWorktree`); commit after each task with the session trailers; never push; never merge until the user says "merge".
- `./test.sh` (gofmt, vet, tests, `-count=1`) must pass after every task; `./test.sh -cross` (windows type-check) after Tasks 1, 3 and 4. This session cannot run Windows programs (WSL interop is off): everything Windows-only is unit-tested with GOOS injected and verified live by the user (Task 5).
- Quoting stays POSIX everywhere (`provider.ShellQuote`, `tools.shellQuote` unchanged).
- Handle format for wezterm: `<pane id>@<tab title>`. Column `run.TmuxTarget` keeps its name.
- Behaviour on Linux/tmux must not change: same tmux commands, same `cmd.sh`, same preamble text with the same binary path.

---

### Task 1: `spawn.Driver`, the two hosts, `NewDriver`

**Files:**
- Create: `internal/spawn/driver.go`
- Create: `internal/spawn/driver_test.go`
- Modify: `internal/spawn/view.go` (no code change; `SweepViews` already exists)

**Interfaces:**
- Produces:
  ```go
  type Driver interface {
      Spawner
      Name() string
      WriteCommand(runDir, command string) (cmdFile string, err error)
      PromptByPaste() bool
      AgentBin(bin string) string
      ShellTool() string
      Viewer() (Viewer, bool)
      Sweep() []string
      OpenTerminal(dir string, argv []string) (launch []string, ok bool)
  }
  func NewDriver(terminal, weztermBin, goos string, run CmdRunner, in StdinRunner) (Driver, error)
  func DriverFor(sp Spawner) Driver          // posix host, Name "tmux", no viewer unless sp is one
  type StdinRunner func(stdin, name string, args ...string) ([]byte, error)
  func ExecStdinRunner(stdin, name string, args ...string) ([]byte, error)
  ```
- `NewWezterm` is defined in Task 2; Task 1 leaves a TODO-free stub: `NewDriver` returns `errors.New("wezterm: not built yet")` for `terminal == "wezterm"` until Task 2 replaces that line. (The test for the wezterm branch lives in Task 2.)

- [ ] **Step 1: Write the failing tests**

`internal/spawn/driver_test.go`:

```go
package spawn

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewDriverPicksTmuxOnLinux(t *testing.T) {
	d, err := NewDriver("", "wezterm", "linux", (&recorder{}).run, nil)
	if err != nil || d.Name() != "tmux" {
		t.Fatalf("driver = %v, %v", d, err)
	}
	if _, ok := d.Viewer(); !ok {
		t.Error("tmux driver must offer a viewer")
	}
	if d.PromptByPaste() {
		t.Error("posix host puts the prompt on the command line")
	}
	if got := d.AgentBin(`/mnt/t/erbrus/bin/erbrus`); got != `/mnt/t/erbrus/bin/erbrus` {
		t.Errorf("AgentBin = %q", got)
	}
	if d.ShellTool() != "${SHELL:-bash}" {
		t.Errorf("ShellTool = %q", d.ShellTool())
	}
	if _, ok := d.OpenTerminal("/x", []string{"bash"}); ok {
		t.Error("tmux opens terminal tools as tool runs, not launches")
	}
}

func TestNewDriverRejectsUnknownTerminal(t *testing.T) {
	if _, err := NewDriver("screen", "", "linux", nil, nil); err == nil || !strings.Contains(err.Error(), `terminal "screen"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestPosixWriteCommand(t *testing.T) {
	dir := t.TempDir()
	p, err := posixHost{}.WriteCommand(dir, "claude --model 'x y'")
	if err != nil || p != filepath.Join(dir, "cmd.sh") {
		t.Fatalf("p = %q, %v", p, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "#!/bin/sh\nexec claude --model 'x y'\n" {
		t.Errorf("cmd.sh = %q", b)
	}
}

func TestWindowsHost(t *testing.T) {
	h := windowsHost{}
	dir := t.TempDir()
	p, err := h.WriteCommand(dir, `claude --model 'sonnet 4' --settings "C:\r\s.json"`)
	if err != nil || p != filepath.Join(dir, "cmd.json") {
		t.Fatalf("p = %q, %v", p, err)
	}
	b, _ := os.ReadFile(p)
	want := "[\"claude\",\"--model\",\"sonnet 4\",\"--settings\",\"C:\\\\r\\\\s.json\"]"
	if strings.TrimSpace(string(b)) != want {
		t.Errorf("cmd.json = %s\nwant %s", b, want)
	}
	if !h.PromptByPaste() {
		t.Error("windows host must paste the prompt")
	}
	if got := h.AgentBin(`T:\others\erbrus\bin\erbrus.exe`); got != "T:/others/erbrus/bin/erbrus.exe" {
		t.Errorf("AgentBin = %q", got)
	}
	if h.ShellTool() != "powershell" {
		t.Errorf("ShellTool = %q", h.ShellTool())
	}
	if _, err := h.WriteCommand(dir, `x "unterminated`); err == nil {
		t.Error("bad quoting must be an error")
	}
}

func TestDriverForWrapsAFakeSpawner(t *testing.T) {
	rec := &recorder{}
	d := DriverFor(NewTmux(rec.run))
	if d.Name() != "tmux" {
		t.Errorf("Name = %q", d.Name())
	}
	if _, ok := d.Viewer(); !ok {
		t.Error("a real tmux keeps its viewer through DriverFor")
	}
	if !reflect.DeepEqual(d.Sweep(), []string(nil)) && len(d.Sweep()) != 0 {
		t.Error("sweep on a recorder must kill nothing")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/spawn/ -run 'Driver|Host' 2>&1 | head`
Expected: compile errors (`undefined: NewDriver`, `posixHost`, `windowsHost`, `DriverFor`).

- [ ] **Step 3: Implement**

`internal/spawn/driver.go`:

```go
package spawn

import (
	"encoding/json"
	"errors"
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
		return nil, errors.New("wezterm: not built yet") // Task 2 replaces this line
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

// posixHost: sh runs cmd.sh; the prompt may sit on the command line.
type posixHost struct{}

func (posixHost) WriteCommand(runDir, command string) (string, error) {
	p := filepath.Join(runDir, "cmd.sh")
	return p, os.WriteFile(p, []byte("#!/bin/sh\nexec "+command+"\n"), 0o755)
}
func (posixHost) PromptByPaste() bool       { return false }
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
func (windowsHost) PromptByPaste() bool       { return true }
func (windowsHost) AgentBin(bin string) string { return strings.ReplaceAll(bin, `\`, "/") }
func (windowsHost) ShellTool() string          { return "powershell" }
```

Until Task 2 exists, add at the bottom a placeholder type so `driver.go` compiles:

```go
// Wezterm is built in Task 2.
type Wezterm struct{}

func (w *Wezterm) OpenTerminal(dir string, argv []string) ([]string, bool) { return nil, false }
```

(Task 2 replaces both lines with the real type.) Note `AgentBin` uses `strings.ReplaceAll`, not `filepath.ToSlash`: the tests run on Linux where `ToSlash` is a no-op.

Check the import cycle first: `go list -deps ./internal/tools | grep spawn` must print nothing (tools imports fmt/regexp/strings only).

- [ ] **Step 4: Run tests**

Run: `go test -count=1 ./internal/spawn/`
Expected: PASS (all existing tmux tests and the new ones).

- [ ] **Step 5: Commit**

```
git add internal/spawn/driver.go internal/spawn/driver_test.go
git commit -m "feat(spawn): Driver — Spawner + host (posix/windows) + mux extras behind one interface"
```

---

### Task 2: The WezTerm spawner

**Files:**
- Create: `internal/spawn/wezterm.go`
- Create: `internal/spawn/wezterm_test.go`
- Modify: `internal/spawn/tmux.go` (`SendChecked` body → shared `sendChecked`)
- Modify: `internal/spawn/driver.go` (replace the placeholder `Wezterm` and the "not built yet" line)

**Interfaces:**
- Consumes: `StdinRunner`, `Driver`, `pendingInInputBox`, `sendSettle`, `sendVerifyDelay`, `sendEnterTries`.
- Produces: `NewWezterm(run StdinRunner, bin string) *Wezterm`; `*Wezterm` implements `Spawner` + `SendChecked` (the server's `checkedSender`) + `OpenTerminal`.

- [ ] **Step 1: Write the failing tests**

`internal/spawn/wezterm_test.go`:

```go
package spawn

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// wrec records stdin-runner calls as "stdin|name args…" and replays
// scripted stdout by command prefix (on "name args…").
type wrec struct {
	calls []string
	out   map[string]string
	fail  map[string]error
	// seq lets one prefix answer differently per call (Send retries).
	seq map[string][]string
}

func (r *wrec) run(stdin, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, stdin+"|"+call)
	for p, err := range r.fail {
		if strings.HasPrefix(call, p) {
			return nil, err
		}
	}
	for p, outs := range r.seq {
		if strings.HasPrefix(call, p) && len(outs) > 0 {
			o := outs[0]
			r.seq[p] = outs[1:]
			return []byte(o), nil
		}
	}
	for p, o := range r.out {
		if strings.HasPrefix(call, p) {
			return []byte(o), nil
		}
	}
	return nil, nil
}

const listJSON = `[
 {"window_id":0,"tab_id":0,"pane_id":0,"workspace":"default","size":{"rows":24,"cols":80},"title":"cmd.exe","cwd":"file:///C:/Users/homee/","tab_title":""},
 {"window_id":1,"tab_id":1,"pane_id":7,"workspace":"erbrus-webshop","size":{"rows":40,"cols":120},"title":"claude","cwd":"file:///T:/code/webshop/","tab_title":"webshop/claude"}
]`

func wspec() RunSpec {
	return RunSpec{Session: "erbrus-webshop", WindowName: "webshop/claude", Workdir: `T:\code\webshop`,
		Env:     map[string]string{"ERBRUS_URL": "http://127.0.0.1:7421"},
		Command: []string{`T:\erbrus\bin\erbrus.exe`, "wrap", `T:\data\runs\7\cmd.json`}}
}

func TestWeztermSpawnNewWindowThenTitle(t *testing.T) {
	r := &wrec{out: map[string]string{"wezterm cli spawn": "7\n"}}
	w := NewWezterm(r.run, "wezterm")
	h, err := w.Spawn(wspec())
	if err != nil || h != "7@webshop/claude" {
		t.Fatalf("h = %q, %v", h, err)
	}
	want := []string{
		`|wezterm cli spawn --new-window --workspace erbrus-webshop --cwd T:\code\webshop -- T:\erbrus\bin\erbrus.exe wrap T:\data\runs\7\cmd.json`,
		`|wezterm cli set-tab-title --pane-id 7 webshop/claude`,
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestWeztermSpawnUsesAttachSessionAsWorkspaceAndReportsStderr(t *testing.T) {
	r := &wrec{out: map[string]string{"wezterm cli spawn": "3\n"}}
	w := NewWezterm(r.run, `C:\Users\homee\bin\WezTerm\wezterm.exe`)
	sp := wspec()
	sp.AttachSession = "shared"
	if _, err := w.Spawn(sp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.calls[0], `C:\Users\homee\bin\WezTerm\wezterm.exe cli spawn --new-window --workspace shared `) {
		t.Errorf("call = %q", r.calls[0])
	}
	r2 := &wrec{fail: map[string]error{"wezterm cli spawn": errors.New("exit status 1")}}
	if _, err := NewWezterm(r2.run, "wezterm").Spawn(wspec()); err == nil || !strings.Contains(err.Error(), "spawn") {
		t.Errorf("err = %v", err)
	}
}

func TestWeztermAliveMatchesIdAndTitle(t *testing.T) {
	r := &wrec{out: map[string]string{"wezterm cli list": listJSON}}
	w := NewWezterm(r.run, "wezterm")
	for h, want := range map[Handle]bool{"7@webshop/claude": true, "7@other": false, "9@webshop/claude": false} {
		if ok, err := w.Alive(h); err != nil || ok != want {
			t.Errorf("Alive(%s) = %v, %v; want %v", h, ok, err, want)
		}
	}
	r2 := &wrec{fail: map[string]error{"wezterm cli list": errors.New("boom")}}
	if _, err := NewWezterm(r2.run, "wezterm").Alive("7@webshop/claude"); err == nil {
		t.Error("a failed list is an error, not 'dead'")
	}
}

func TestWeztermCaptureScreenAndSize(t *testing.T) {
	r := &wrec{out: map[string]string{
		"wezterm cli list":     listJSON,
		"wezterm cli get-text": "T:\\code\\webshop>echo hello\nhello\n",
	}}
	w := NewWezterm(r.run, "wezterm")
	sc, err := w.Capture("7@webshop/claude")
	if err != nil || sc.Cols != 120 || sc.Rows != 40 || sc.Dead || !sc.Activity.IsZero() {
		t.Fatalf("sc = %+v, %v", sc, err)
	}
	if !strings.Contains(sc.Raw, "echo hello") {
		t.Errorf("raw = %q", sc.Raw)
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "|wezterm cli get-text --escapes --pane-id 7") {
		t.Errorf("calls = %q", r.calls)
	}
	// Gone pane: not in the list → error, no get-text.
	if _, err := w.Capture("9@x"); err == nil {
		t.Error("capture of a gone pane must fail")
	}
}

func TestWeztermSendPastesOnStdinThenEnter(t *testing.T) {
	sendSettle, sendVerifyDelay = 0, 0
	r := &wrec{out: map[string]string{"wezterm cli get-text": "❯ \n"}}
	w := NewWezterm(r.run, "wezterm")
	if err := w.Send("7@webshop/claude", "hello\nworld"); err != nil {
		t.Fatal(err)
	}
	if r.calls[0] != "hello\nworld|wezterm cli send-text --pane-id 7" {
		t.Errorf("paste = %q", r.calls[0])
	}
	if r.calls[1] != "\r|wezterm cli send-text --no-paste --pane-id 7" {
		t.Errorf("enter = %q", r.calls[1])
	}
}

func TestWeztermSendRetriesWhileTextSitsInInputBox(t *testing.T) {
	sendSettle, sendVerifyDelay = 0, 0
	pending := "──────────\n❯ hello\n"
	r := &wrec{seq: map[string][]string{"wezterm cli get-text": {pending, pending, "❯ \n"}}}
	w := NewWezterm(r.run, "wezterm")
	if err := w.Send("7@x", "hello"); err != nil {
		t.Fatal(err)
	}
	enters := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, "\r|") {
			enters++
		}
	}
	if enters != 3 {
		t.Errorf("enters = %d, want 3", enters)
	}
}

func TestWeztermSendKeys(t *testing.T) {
	r := &wrec{}
	w := NewWezterm(r.run, "wezterm")
	for key, want := range map[string]string{"Enter": "\r", "Escape": "\x1b", "Up": "\x1b[A", "Down": "\x1b[B", "Tab": "\t", "3": "3"} {
		r.calls = nil
		if err := w.SendKeys("7@x", key); err != nil {
			t.Fatal(err)
		}
		if r.calls[0] != want+"|wezterm cli send-text --no-paste --pane-id 7" {
			t.Errorf("%s → %q", key, r.calls[0])
		}
	}
	if err := w.SendKeys("7@x", "C-c"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %v", err)
	}
}

func TestWeztermStopAndOpenTerminal(t *testing.T) {
	r := &wrec{}
	w := NewWezterm(r.run, "wezterm")
	if err := w.Stop("7@x"); err != nil || r.calls[0] != "|wezterm cli kill-pane --pane-id 7" {
		t.Fatalf("stop: %v %q", err, r.calls)
	}
	launch, ok := w.OpenTerminal(`T:\code\webshop`, []string{"powershell"})
	if !ok || !reflect.DeepEqual(launch, []string{"wezterm", "start", "--cwd", `T:\code\webshop`, "--", "powershell"}) {
		t.Errorf("open = %q %v", launch, ok)
	}
}

func TestNewDriverPicksWeztermOnWindows(t *testing.T) {
	d, err := NewDriver("", "wezterm", "windows", nil, (&wrec{}).run)
	if err != nil || d.Name() != "wezterm" {
		t.Fatalf("driver = %v, %v", d, err)
	}
	if _, ok := d.Viewer(); ok {
		t.Error("wezterm has no browser terminal")
	}
	if !d.PromptByPaste() {
		t.Error("windows host pastes")
	}
	if launch, ok := d.OpenTerminal("/x", []string{"gg"}); !ok || launch[0] != "wezterm" {
		t.Errorf("open = %q %v", launch, ok)
	}
	// Explicit wezterm on linux is allowed too (posix host).
	d, err = NewDriver("wezterm", "/opt/wezterm", "linux", nil, (&wrec{}).run)
	if err != nil || d.Name() != "wezterm" || d.PromptByPaste() {
		t.Errorf("linux+wezterm = %v %v", d, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/spawn/ -run Wezterm 2>&1 | head -5`
Expected: `undefined: NewWezterm`.

- [ ] **Step 3: Refactor `SendChecked` into a shared function**

In `tmux.go`, replace the body of `SendChecked` with a call to the shared helper; the paste/enter/capture closures keep the exact tmux commands:

```go
func (t *Tmux) SendChecked(h Handle, text string, submitted func(screen string) bool) error {
	paste := func() error {
		if _, err := t.run("tmux", "set-buffer", "--", text); err != nil {
			return fmt.Errorf("send to %s: %w", h, err)
		}
		if _, err := t.run("tmux", "paste-buffer", "-dp", "-t", exact(string(h))); err != nil {
			return fmt.Errorf("send to %s: %w", h, err)
		}
		return nil
	}
	enter := func() error {
		if _, err := t.run("tmux", "send-keys", "-t", exact(string(h)), "Enter"); err != nil {
			return fmt.Errorf("send Enter to %s: %w", h, err)
		}
		return nil
	}
	capture := func() (string, error) {
		out, err := t.run("tmux", "capture-pane", "-p", "-t", exact(string(h)))
		return string(out), err
	}
	return sendChecked(string(h), text, submitted, paste, enter, capture)
}

// sendChecked is the delivery shared by every spawner: paste, settle,
// Enter, then check the screen and press again while the text still sits
// in the input box. A capture error ends the attempt without more Enters.
func sendChecked(h, text string, submitted func(string) bool, paste, enter func() error, capture func() (string, error)) error {
	if err := paste(); err != nil {
		return err
	}
	time.Sleep(sendSettle)
	for i := 0; i < sendEnterTries; i++ {
		if err := enter(); err != nil {
			return err
		}
		time.Sleep(sendVerifyDelay)
		out, err := capture()
		if err != nil {
			return nil // cannot tell: do not spam Enter
		}
		if submitted != nil && submitted(out) {
			return nil
		}
		if !pendingInInputBox(out, text) {
			return nil
		}
	}
	return fmt.Errorf("message to %s was pasted but the agent did not submit it after %d Enter presses — press Enter in its terminal", h, sendEnterTries)
}
```

Run: `go test -count=1 ./internal/spawn/ -run 'Send'` → PASS (tmux behaviour unchanged).

- [ ] **Step 4: Implement `wezterm.go`**

```go
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
	w.cli("", "set-tab-title", "--pane-id", id, spec.WindowName) // cosmetic
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

func (w *Wezterm) SendKeys(h Handle, key string) error {
	b, ok := keyBytes[key]
	if !ok {
		if len([]rune(key)) != 1 {
			return fmt.Errorf("key %q not supported by the wezterm spawner", key)
		}
		b = key
	}
	id, _ := splitHandle(h)
	if _, err := w.cli(b, "send-text", "--no-paste", "--pane-id", id); err != nil {
		return fmt.Errorf("send key %q to %s: %w", key, h, err)
	}
	return nil
}

func (w *Wezterm) Stop(h Handle) error {
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
```

In `driver.go`: delete the placeholder `Wezterm` type and its method; replace the "not built yet" line with

```go
	case "wezterm":
		if weztermBin == "" {
			weztermBin = "wezterm"
		}
		return &driver{Spawner: NewWezterm(in, weztermBin), host: h, name: "wezterm"}, nil
```

- [ ] **Step 5: Run tests**

Run: `go test -count=1 ./internal/spawn/` → PASS. Then `gofmt -l internal/spawn` → nothing.

- [ ] **Step 6: Commit**

```
git add internal/spawn/wezterm.go internal/spawn/wezterm_test.go internal/spawn/tmux.go internal/spawn/driver.go
git commit -m "feat(spawn): WezTerm spawner (headless mux via wezterm cli); sendChecked shared with tmux"
```

---

### Task 3: Env file + `erbrus wrap` runs `.json` argv without a shell

**Files:**
- Modify: `internal/cli/wrap.go`
- Modify: `internal/cli/wrap_test.go`
- Create: `internal/spawn/envfile.go` (writer, used by the server in Task 4; reader used by wrap)
- Create: `internal/spawn/envfile_test.go`

**Interfaces:**
- Produces: `spawn.WriteEnvFile(runDir string, env map[string]string) error` (file `<runDir>/env`, `KEY=VALUE\n`, sorted keys); `spawn.ReadEnvFile(cmdFile string) ([]string, error)` (`KEY=VALUE` lines from `<dir of cmdFile>/env`; `nil, nil` when the file does not exist).

- [ ] **Step 1: Write the failing tests**

`internal/spawn/envfile_test.go`:

```go
package spawn

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnvFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEnvFile(dir, map[string]string{"ERBRUS_URL": "http://127.0.0.1:7421", "ERBRUS_RUN_ID": "7"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "env"))
	if string(b) != "ERBRUS_RUN_ID=7\nERBRUS_URL=http://127.0.0.1:7421\n" {
		t.Errorf("env = %q", b)
	}
	got, err := ReadEnvFile(filepath.Join(dir, "cmd.json"))
	if err != nil || !reflect.DeepEqual(got, []string{"ERBRUS_RUN_ID=7", "ERBRUS_URL=http://127.0.0.1:7421"}) {
		t.Errorf("read = %q, %v", got, err)
	}
	if got, err := ReadEnvFile(filepath.Join(t.TempDir(), "cmd.sh")); err != nil || got != nil {
		t.Errorf("missing file: %q, %v", got, err)
	}
}
```

Append to `internal/cli/wrap_test.go`:

```go
func TestWrapLoadsEnvFileAndRunsArgvJSON(t *testing.T) {
	var gotCode = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		gotCode = body["code"]
	}))
	defer srv.Close()
	wrapEnv(t, srv.URL)
	dir := t.TempDir()
	// The env file overrides the inherited ERBRUS_RUN_ID=7 with 42.
	os.WriteFile(filepath.Join(dir, "env"), []byte("ERBRUS_RUN_ID=42\nFROM_FILE=yes\n"), 0o644)
	// argv, no shell: an argument with a space stays one argument.
	os.WriteFile(filepath.Join(dir, "cmd.json"), []byte(`["sh","-c","echo \"$FROM_FILE $1\"","x","a b"]`), 0o644)
	var out, errOut bytes.Buffer
	if code := Run([]string{"wrap", filepath.Join(dir, "cmd.json")}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "yes a b" {
		t.Errorf("stdout = %q", out.String())
	}
	if gotCode != 0 {
		t.Errorf("reported code = %d", gotCode)
	}
}

func TestWrapEnvFileAlsoAppliesToShellScripts(t *testing.T) {
	wrapEnv(t, "http://127.0.0.1:1")
	p := writeCmdFile(t, `echo "$FROM_FILE"`)
	os.WriteFile(filepath.Join(filepath.Dir(p), "env"), []byte("FROM_FILE=sh-too\n"), 0o644)
	var out, errOut bytes.Buffer
	Run([]string{"wrap", p}, &out, &errOut)
	if strings.TrimSpace(out.String()) != "sh-too" {
		t.Errorf("stdout = %q", out.String())
	}
}
```

(The run id the wrapper reports comes from its own environment, `ERBRUS_RUN_ID`; the env file must also feed that lookup: read `runID` from the merged env, not `os.Getenv`. The first test asserts nothing about the id beyond the report arriving; add `if r.URL.Path != "/api/runs/42/exit" { t.Errorf(...) }` inside the handler to pin it — check the actual path `client.ReportExit` posts to with `grep -n ReportExit internal/client/client.go` and use that.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/spawn/ -run EnvFile; go test ./internal/cli/ -run Wrap`
Expected: `undefined: WriteEnvFile`; the wrap tests fail (`sh cmd.json` errors / env not applied).

- [ ] **Step 3: Implement**

`internal/spawn/envfile.go`:

```go
package spawn

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The env file carries a run's ERBRUS_* variables to `erbrus wrap` on
// every platform: WezTerm's spawn has no env flag, tmux's -e stays as a
// courtesy to humans in shell windows.

func WriteEnvFile(runDir string, env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + env[k] + "\n")
	}
	return os.WriteFile(filepath.Join(runDir, "env"), []byte(b.String()), 0o600)
}

// ReadEnvFile returns KEY=VALUE lines from <dir of cmdFile>/env; nil when
// there is no such file.
func ReadEnvFile(cmdFile string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(cmdFile), "env"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimRight(line, "\r"); strings.Contains(line, "=") {
			out = append(out, line)
		}
	}
	return out, nil
}
```

`internal/cli/wrap.go` — replace `runWrap`:

```go
func runWrap(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: erbrus wrap <cmdfile>")
		return 2
	}
	cmdFile := args[0]
	fileEnv, err := spawn.ReadEnvFile(cmdFile)
	if err != nil {
		fmt.Fprintln(stderr, "wrap:", err)
		return 127
	}
	env := append(os.Environ(), fileEnv...) // later entries win in exec
	for _, kv := range fileEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			os.Setenv(k, v) // so ERBRUS_RUN_ID / client.FromEnv see the file too
		}
	}

	var cmd *exec.Cmd
	if strings.HasSuffix(cmdFile, ".json") {
		b, err := os.ReadFile(cmdFile)
		var argv []string
		if err == nil {
			err = json.Unmarshal(b, &argv)
		}
		if err != nil || len(argv) == 0 {
			fmt.Fprintln(stderr, "wrap: bad argv file:", err)
			return 127
		}
		cmd = exec.Command(argv[0], argv[1:]...)
	} else {
		cmd = exec.Command("sh", cmdFile)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			fmt.Fprintln(stderr, "wrap:", err)
			code = 127
		}
	}

	runID, _ := strconv.ParseInt(os.Getenv("ERBRUS_RUN_ID"), 10, 64)
	c, _, _ := client.FromEnv()
	if runID != 0 {
		if err := c.ReportExit(runID, code); err != nil {
			fmt.Fprintln(stderr, "wrap: exit report failed:", err)
		}
	}
	return code
}
```

Add imports `encoding/json`, `strings`, `erbrus/internal/spawn`. Check `internal/cli` does not already import something that imports `cli` back (`go list -deps ./internal/spawn | grep internal/cli` → nothing).

- [ ] **Step 4: Run tests**

Run: `go test -count=1 ./internal/spawn/ ./internal/cli/` → PASS.

- [ ] **Step 5: Commit**

```
git add internal/spawn/envfile.go internal/spawn/envfile_test.go internal/cli/wrap.go internal/cli/wrap_test.go
git commit -m "feat(wrap): run env from <runDir>/env; .json command files run as bare argv (no shell)"
```

---

### Task 4: The server uses the driver

**Files:**
- Modify: `internal/server/server.go` (field `driver spawn.Driver`; `SetRuntime` wraps with `DriverFor`; new `SetDriver`)
- Modify: `internal/server/runs.go` (`spawnerKind`, `WriteCommand`, env file, `PromptByPaste`, `AgentBin`, note text, `Reconcile` selection)
- Modify: `internal/server/watch.go:49` (selection)
- Modify: `internal/server/screen.go:221`, `internal/server/terminal.go:56` (`driver.Viewer()`)
- Modify: `internal/server/ui_tools.go` (`OpenTerminal`)
- Modify: `internal/server/ui_channel.go:390`, `internal/server/ui_forward.go:190` (`AgentBin`)
- Modify: `internal/server/config_live.go` or wherever `BuiltinTools` is merged — the shell template comes from the driver (`s.driver.ShellTool()`); `config.BuiltinTools()` keeps `${SHELL:-bash}` as the config-package default and the server overrides `tools.shell.Command` when the user has not defined `shell` themselves — simplest: `config.BuiltinToolsFor(shell string)` and `config.BuiltinTools()` = `BuiltinToolsFor("${SHELL:-bash}")`; `LoadGlobal` keeps using `BuiltinTools()`; `ReloadConfig`/`toolsSnapshot` in the server re-merge with `BuiltinToolsFor(s.driver.ShellTool())` when the driver is set and the user did not define `shell`.
- Modify: `internal/cli/serve.go` (`spawn.NewDriver(cfg.Terminal, cfg.WeztermBin, runtime.GOOS, spawn.ExecCmdRunner, spawn.ExecStdinRunner)`; `srv.SetDriver(d, bin, url)`; `d.Sweep()`)
- Modify: `internal/config/config.go` (`WeztermBin string \`yaml:"wezterm_bin"\``; `Defaults` leaves `Terminal` empty — the driver applies the platform default — and `LoadGlobal` accepts `""|tmux|wezterm`, error otherwise)
- Modify: `internal/server/ui_settings.go` scaffold (`terminal: tmux | wezterm`, `wezterm_bin`, the Windows editor note already there)
- Tests: `internal/server/driver_test.go` (new), `internal/server/config_reload_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–3.
- Produces: `func (s *Server) SetDriver(d spawn.Driver, erbrusBin, baseURL string)`; `SetRuntime(sp, bin, url)` = `SetDriver(spawn.DriverFor(sp), bin, url)` so the existing tests keep working; `s.spawner` stays (assigned from the driver) for the untouched call sites.

- [ ] **Step 1: Write the failing tests**

`internal/server/driver_test.go`:

```go
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
)

// withProvider sets one provider on the test server (pattern of
// paste_test.go: cfg.Providers under rulesMu).
func withProvider(t *testing.T, name string, p config.Provider) {
	t.Helper()
	testSrv.rulesMu.Lock()
	testSrv.cfg.Providers[name] = p
	testSrv.rulesMu.Unlock()
}

// fakeDriver: a fakeSpawner with Windows-style host answers, so the
// server's platform seams show up on Linux.
type fakeDriver struct {
	*fakeSpawner
	name  string
	paste bool
	open  bool
}

func (d *fakeDriver) Name() string { return d.name }
func (d *fakeDriver) WriteCommand(runDir, command string) (string, error) {
	p := filepath.Join(runDir, "cmd.json")
	return p, os.WriteFile(p, []byte("[\""+command+"\"]\n"), 0o644)
}
func (d *fakeDriver) PromptByPaste() bool          { return d.paste }
func (d *fakeDriver) AgentBin(bin string) string  { return strings.ReplaceAll(bin, `\`, "/") }
func (d *fakeDriver) ShellTool() string           { return "powershell" }
func (d *fakeDriver) Viewer() (spawn.Viewer, bool) { return nil, false }
func (d *fakeDriver) Sweep() []string             { return nil }
func (d *fakeDriver) OpenTerminal(dir string, argv []string) ([]string, bool) {
	if !d.open {
		return nil, false
	}
	return append([]string{"wezterm", "start", "--cwd", dir, "--"}, argv...), true
}

func TestSpawnThroughDriverWritesArgvFileEnvFileAndPastes(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "7@root/claude"}
	d := &fakeDriver{fakeSpawner: fs, name: "wezterm", paste: true}
	testSrv.SetDriver(d, `T:\erbrus\bin\erbrus.exe`, ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	withProvider(t, "claude-code", config.Provider{Command: `claude --model {model} "{prompt}"`, Prompt: "arg", ScreenWaiting: []string{"❯"}})
	fs.setScreen("7@root/claude", spawn.Screen{Raw: "❯ \n"}) // input box up: the paste goes out at once

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "claude-code", "prompt": "do it"})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("spawn = %d", r.StatusCode)
	}
	runs, _ := st.RunsByChannel(ch1)
	run := runs[0]
	if run.Spawner != "wezterm" {
		t.Errorf("Spawner = %q", run.Spawner)
	}
	cmdFile := fs.specs[0].Command[2]
	if filepath.Base(cmdFile) != "cmd.json" {
		t.Errorf("command file = %s", cmdFile)
	}
	b, _ := os.ReadFile(cmdFile)
	if strings.Contains(string(b), "do it") || strings.Contains(string(b), "erbrus channel") {
		t.Errorf("PromptByPaste driver must keep the prompt off the command line: %s", b)
	}
	env, _ := os.ReadFile(filepath.Join(filepath.Dir(cmdFile), "env"))
	if !strings.Contains(string(env), "ERBRUS_RUN_ID="+fmt.Sprint(run.ID)) || !strings.Contains(string(env), "ERBRUS_URL="+ts.URL) {
		t.Errorf("env file = %q", env)
	}
	// The pasted preamble spells the binary with forward slashes.
	sent := waitSent(fs, 2*time.Second)
	if len(sent) == 0 {
		t.Fatal("prompt was never pasted")
	}
	if !strings.Contains(sent[0], "T:/erbrus/bin/erbrus.exe msg send") || strings.Contains(sent[0], `T:\erbrus`) {
		t.Errorf("preamble = %q", sent)
	}
}

func TestReconcileAndWatcherCoverNonTmuxRuns(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "7@root/claude", alive: map[spawn.Handle]bool{}}
	testSrv.SetDriver(&fakeDriver{fakeSpawner: fs, name: "wezterm"}, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	withProvider(t, "claude-code", config.Provider{Command: "claude"})
	postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "claude-code", "prompt": "x"})
	if err := testSrv.Reconcile(); err != nil {
		t.Fatal(err)
	}
	runs, _ := st.RunsByChannel(ch1)
	if runs[0].Status != "failed" {
		t.Errorf("a wezterm run whose pane is gone must be failed by Reconcile, got %q", runs[0].Status)
	}
}

func TestTerminalToolLaunchesWhenDriverOpensTerminals(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetDriver(&fakeDriver{fakeSpawner: fs, name: "wezterm", open: true}, "/abs/erbrus", ts.URL)
	fl := &fakeLauncher{}
	testSrv.SetLauncher(fl)
	ch1, _ := twoChannels(t, ts.URL, root)

	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/shell", ts.URL, ch1), nil)
	r.Body.Close()
	if loc := r.Header.Get("Location"); loc != fmt.Sprintf("/ui/channels/%d", ch1) {
		t.Fatalf("redirect = %s", loc)
	}
	if len(fl.calls) != 1 || !strings.HasPrefix(fl.calls[0], root+"|wezterm start --cwd "+root+" -- powershell") {
		t.Fatalf("launch = %v", fl.calls)
	}
	if runs, _ := st.RunsByChannel(ch1); len(runs) != 0 {
		t.Error("no tool run when the driver opens a window")
	}
	page := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if strings.Contains(page, fmt.Sprintf(`target="tool:shell:%d"`, ch1)) {
		t.Error("launch-style terminal tools need no tab target")
	}
}
```

Helpers used: `newTestServer` (server_test.go), `twoChannels` (unread_test.go), `postJSON(t, url, body any)` (server_test.go), `getBody` (tool_run_test.go), `noRedirect` (compose_test.go), `fakeLauncher` (ui_tools_test.go), `waitSent(fs, d)` (paste_test.go), `fs.setScreen` (runs_test.go). `withProvider` is new, defined above. The screen rule field name (`ScreenWaiting`) is the one in `config.Provider`; check with `grep -n "ScreenWaiting\|ScreenWorking" internal/config/config.go`.

Config test, append to `internal/config/config_test.go`:

```go
func TestTerminalValues(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte("terminal: wezterm\nwezterm_bin: C:/w/wezterm.exe\n"), 0o644)
	g, err := LoadGlobal(p)
	if err != nil || g.Terminal != "wezterm" || g.WeztermBin != "C:/w/wezterm.exe" {
		t.Fatalf("g = %+v, %v", g, err)
	}
	os.WriteFile(p, []byte("terminal: screen\n"), 0o644)
	if _, err := LoadGlobal(p); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("err = %v", err)
	}
	if Defaults().Terminal != "" {
		t.Error("the platform default is the driver's call, not config's")
	}
}
```

Reload test, append to `internal/server/config_reload_test.go`: with a driver whose `ShellTool()` is `powershell` and a config without a user `shell` tool, `toolCfg("shell").Command == "powershell"`; with a user `shell: {command: bash, terminal: true}` the user wins.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run 'Driver|NonTmux|DriverOpens' 2>&1 | head; go test ./internal/config/ -run Terminal`
Expected: `undefined: SetDriver`, `WeztermBin`.

- [ ] **Step 3: Implement, file by file**

`config.go`: add `WeztermBin string \`yaml:"wezterm_bin"\`` after `GgBin`; in `Defaults()` remove `Terminal: "tmux"`; in `LoadGlobal` after the Session default:

```go
	switch g.Terminal {
	case "", "tmux", "wezterm":
	default:
		return g, fmt.Errorf("%s: terminal %q: want tmux or wezterm", path, g.Terminal)
	}
```

`BuiltinToolsFor(shell string)`; `BuiltinTools()` calls it with `${SHELL:-bash}`.

`server.go`:

```go
	driver    spawn.Driver
...
// SetRuntime wires a bare spawner (tests): posix host, tmux name.
func (s *Server) SetRuntime(sp spawn.Spawner, erbrusBin, baseURL string) {
	s.SetDriver(spawn.DriverFor(sp), erbrusBin, baseURL)
}

// SetDriver wires the platform driver; the spawner field is the same
// object, kept for the call sites that only spawn/capture/send.
func (s *Server) SetDriver(d spawn.Driver, erbrusBin, baseURL string) {
	s.driver, s.spawner, s.erbrusBin, s.baseURL = d, d, erbrusBin, baseURL
	s.applyShellTool()
}
```

`applyShellTool` (in `config_live.go`): under the config mutex, if the user config has no `shell` tool of its own, set `tools["shell"] = config.BuiltinToolsFor(s.driver.ShellTool())["shell"]`; `ReloadConfig` calls it after swapping tools. To know "user's own", keep the loaded file's raw tools map or compare against `config.BuiltinTools()["shell"]` (equal ⇒ built-in ⇒ replace). Use the comparison; document it in a comment.

`runs.go`:
- `spawnerKind := s.driver.Name()` (guard: `if s.driver != nil`; fg stays `"fg"`).
- Step 7: `paste = pasteMode(providers[providerName]) || s.driver.PromptByPaste()`.
- `agentBin := s.driver.AgentBin(s.erbrusBin)`; use it in `ProviderHook` and `Preamble`.
- Step 8: `cmdPath, err := s.driver.WriteCommand(runDir, command)`; then `spawn.WriteEnvFile(runDir, env)` right after Step 9 builds `env` (before the fg return, so fg gets it too — harmless).
- Note text: `fmt.Sprintf("%s spawned in %s %s", agentName, s.driver.Name(), handle)`.
- `Reconcile`: `if r.TmuxTarget == "" { continue }`; note text `"(%s %s)"` with `r.Spawner`.

`watch.go:49`: same selection change.

`screen.go:221`: `if _, ok := s.driver.Viewer(); ok && page.Live && …` (keep the `runtime.GOOS != "windows"` guard; it is now redundant but harmless — remove it and the `runtime` import if nothing else uses it). `terminal.go:56`: `viewer, ok := s.driver.Viewer()`.

`ui_channel.go:390`, `ui_forward.go:190`: `s.driver.AgentBin(s.erbrusBin)`.

`ui_tools.go`: in the `tl.Terminal` branch, before the spawner check:

```go
		words, err := tools.Split(tl.Command)
		if err != nil { warn(err.Error()); return }
		argv, err := tools.RenderArgv(name, words, s.toolVars(dir))   // dir computed above the branch
		if err != nil { warn(err.Error()); return }
		if launch, ok := s.driver.OpenTerminal(dir, argv); ok {
			if s.launcher == nil { warn("no launcher configured"); return }
			if err := s.launcher.Start(dir, launch); err != nil { warn(err.Error()); return }
			http.Redirect(w, r, back, http.StatusFound)
			return
		}
```

Move the `project`/`dir` computation above the branch. `toolViews()` gains `Target bool` = `Terminal && !driverOpens` so the template only sets `target=` for tool-run terminals: add `func (s *Server) driverOpensTerminals() bool { _, ok := s.driver.OpenTerminal("", nil); return ok }` and use `{{if .Target}}` in `channel.html`.

`serve.go`:

```go
	d, err := spawn.NewDriver(cfg.Terminal, cfg.WeztermBin, runtime.GOOS, spawn.ExecCmdRunner, spawn.ExecStdinRunner)
	if err != nil { fmt.Fprintln(stderr, err); return 1 }
	srv.SetDriver(d, bin, "http://"+ln.Addr().String())
	srv.SetLauncher(server.ExecLauncher())
	if killed := d.Sweep(); len(killed) > 0 { … }
```

Scaffold in `ui_settings.go`:

```
# terminal: tmux                        # tmux (default on Linux) | wezterm (default on Windows)
# wezterm_bin: wezterm                  # path to wezterm when it is not on PATH
```

- [ ] **Step 4: Run the suite**

Run: `./test.sh && ./test.sh -cross`
Expected: OK twice. Existing tests that assert the note "spawned in tmux" or `Spawner == "tmux"` still pass (the fake driver from `DriverFor` is named tmux).

- [ ] **Step 5: Commit**

```
git add -A internal/server internal/config internal/cli/serve.go internal/web/templates/channel.html
git commit -m "feat(server): platform driver — command file, env file, prompt-by-paste, agent bin path, terminal tools and viewer all come from spawn.Driver"
```

---

### Task 5: Docs, build, hand-over, Windows live check

**Files:**
- Modify: `docs/superpowers/specs/2026-09-10-wezterm-spawner-design.md` (amendments)
- Modify: `docs/BACKLOG.md` (Windows verification items; last-screen-after-exit idea)
- Build: `./build.sh` (both binaries; the exe cannot be rebuilt while the user's Windows server runs — say so)

- [ ] **Step 1: Linux regression on the throwaway** — `python3 scratchpad/toolslive.py`-style pass on 7499 with the worktree binary: spawn one agent (claude-code, sonnet) into a scratch repo, confirm the note reads "spawned in tmux", the `env` file exists in the run dir, the preamble arrives, stop it. Nothing may differ from before for tmux.
- [ ] **Step 2: `./test.sh -cross`, `./build.sh`, commit docs.**
- [ ] **Step 3: Hand-over report** with the Windows checklist for the user (this session cannot run Windows programs):
  1. stop the Windows `erbrus serve`; `build.cmd`; in `C:\Users\homee\.config\erbrus\config.yaml` set `port: 7421` (only if the WSL server keeps 7420) — `terminal` needs nothing (wezterm is the Windows default); `providers.claude-code` as on Linux;
  2. `erbrus serve`; open a channel; spawn claude-code; expect the note "claude spawned in wezterm 1@<dir>/claude", the Screen page showing the CLI, the preamble typed in by paste, a report back via `T:/others/erbrus/bin/erbrus.exe msg send` in Git Bash;
  3. send a chat message; stop the run; open `shell` from the Tools rail (a WezTerm window in the directory);
  4. `wezterm cli kill-pane --pane-id N` by hand while a run is live: Reconcile marks it failed within 10 s;
  5. paste back anything that fails, plus `wezterm cli list --format json` at that moment.
- [ ] **Step 4: Do NOT merge.** The user merges after the Windows check.
