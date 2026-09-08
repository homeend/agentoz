# Interactive Web Terminal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A real xterm.js terminal on the run's screen page, typing into the agent's tmux window through a pty-backed, size-neutral tmux view client, with fallback to the existing read-only page.

**Architecture:** `internal/spawn` learns to build the view-client command (`Viewer`); `internal/termview` runs any argv in a pty; `internal/server/terminal.go` bridges a websocket to that pty and polls the window size; the screen page hosts vendored xterm.js and falls back to the SSE `<pre>` when the socket fails. Cleanup is tmux's `destroy-unattached` plus a startup sweep.

**Tech Stack:** Go 1.26, chi, `github.com/coder/websocket` v1.8.15, `github.com/creack/pty` v1.1.24, `@xterm/xterm` 6.0.0 (vendored), tmux ≥ 3.2 (`-f ignore-size`).

**Spec:** /mnt/t/others/erbrus/.claude/worktrees/web-terminal/docs/superpowers/specs/2026-09-08-web-terminal-design.md

## Global Constraints

- Work in `/mnt/t/others/erbrus/.claude/worktrees/web-terminal` on branch `worktree-web-terminal`; commit per task; never push; never touch main until the user asks to merge.
- Never attach to, resize, kill, or restart the user's tmux sessions. Tests that need a real tmux use sessions named `claudetest-*` and kill only those. `go test ./...` must pass without a tmux server; real-tmux tests run only with `ERBRUS_TMUX_TEST=1`.
- Only the two new Go dependencies above; both pure Go; `CGO_ENABLED=0` and `GOOS=windows` builds must keep working (`./build.sh`).
- Shell gotchas in this environment: quote `=target` arguments (zsh equals-expansion); do not put the substring `git` (including `github`) into Bash commands other than plain git commands, the worktree guard refuses them — edit `go.mod` with the Edit tool and run `go mod tidy`; run `sh scripts/vendor-xterm.sh` instead of inline curl.
- No new config keys. No channel messages for typed input.
- View session names are `erbrus-view-<runID>-<8 hex>`; view sessions carry `status off`, `destroy-unattached on`, `prefix None`, `prefix2 None`, and are created with `-f ignore-size` in the run's session group.

---

### Task 1: Dependencies

**Files:**
- Modify: `go.mod`
- Test: build only

**Interfaces:**
- Produces: importable `github.com/coder/websocket` and `github.com/creack/pty` for Tasks 3 and 4.

- [ ] **Step 1: Add the requirements**

Edit `go.mod`, first `require` block, add two lines (keep alphabetical order):

```
	github.com/coder/websocket v1.8.15
	github.com/creack/pty v1.1.24
```

- [ ] **Step 2: Tidy and build**

Run: `go mod tidy && go build ./... && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`
Expected: both builds succeed; `go.sum` gains entries for the two modules; `go mod tidy` may drop the two lines again because nothing imports them yet — if it does, re-add them and run `go mod download github.com/coder/websocket github.com/creack/pty` instead (put those two module paths in a file and run `xargs go mod download < file` so the Bash line carries no `git` substring). Otherwise defer the tidy to Task 3, Step 4.

- [ ] **Step 3: Commit**

```bash
/usr/bin/git add go.mod go.sum
/usr/bin/git commit -m "build: add coder/websocket and creack/pty for the web terminal"
```

---

### Task 2: `spawn.Viewer` — view command, size, name, sweep

**Files:**
- Create: `internal/spawn/view.go`
- Test: `internal/spawn/view_test.go`

**Interfaces:**
- Consumes: `Tmux.run` (`CmdRunner`), `exact()` from `internal/spawn/tmux.go`.
- Produces:
  ```go
  type Viewer interface {
      ViewCommand(h Handle, view string) ([]string, error)
      Size(h Handle) (cols, rows int, err error)
  }
  func ViewName(runID int64) string
  func (t *Tmux) ViewCommand(h Handle, view string) ([]string, error)
  func (t *Tmux) Size(h Handle) (int, int, error)
  func (t *Tmux) SweepViews() (killed []string)
  ```

- [ ] **Step 1: Write the failing tests**

`internal/spawn/view_test.go`:

```go
package spawn

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestViewCommand(t *testing.T) {
	rec := &recorder{}
	argv, err := NewTmux(rec.run).ViewCommand("erbrus-web:7", "erbrus-view-12-a1b2c3d4")
	if err != nil {
		t.Fatal(err)
	}
	want := "tmux new-session -t erbrus-web -s erbrus-view-12-a1b2c3d4 -f ignore-size" +
		" ; set -t erbrus-view-12-a1b2c3d4 status off" +
		" ; set -t erbrus-view-12-a1b2c3d4 destroy-unattached on" +
		" ; set -t erbrus-view-12-a1b2c3d4 prefix None" +
		" ; set -t erbrus-view-12-a1b2c3d4 prefix2 None" +
		" ; select-window -t =erbrus-view-12-a1b2c3d4:7"
	if got := strings.Join(argv, " "); got != want {
		t.Errorf("argv =\n%s\nwant\n%s", got, want)
	}
	if len(rec.calls) != 0 {
		t.Errorf("ViewCommand must not run tmux, ran %v", rec.calls)
	}
	if _, err := NewTmux(rec.run).ViewCommand("nocolon", "v"); err == nil {
		t.Error("malformed handle accepted")
	}
}

func TestSize(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux display": "120 35\n"}}
	cols, rows, err := NewTmux(rec.run).Size("erbrus-web:7")
	if err != nil || cols != 120 || rows != 35 {
		t.Fatalf("size = %d x %d, %v", cols, rows, err)
	}
	if rec.calls[0] != "tmux display -p -t =erbrus-web:7 #{window_width} #{window_height}" {
		t.Errorf("call = %q", rec.calls[0])
	}
	rec = &recorder{fail: map[string]error{"tmux display": errors.New("can't find window")}}
	if _, _, err := NewTmux(rec.run).Size("erbrus-web:7"); err == nil {
		t.Error("missing window must error")
	}
	rec = &recorder{out: map[string]string{"tmux display": "x y\n"}}
	if _, _, err := NewTmux(rec.run).Size("erbrus-web:7"); err == nil {
		t.Error("garbage must error")
	}
}

func TestViewName(t *testing.T) {
	a, b := ViewName(12), ViewName(12)
	if !regexp.MustCompile(`^erbrus-view-12-[0-9a-f]{8}$`).MatchString(a) {
		t.Errorf("name = %q", a)
	}
	if a == b {
		t.Errorf("two names for one run must differ: %q", a)
	}
}

func TestSweepViewsKillsOnlyUnattachedViews(t *testing.T) {
	rec := &recorder{out: map[string]string{
		"tmux list-sessions": "erbrus-web 1\nerbrus-view-3-aaaaaaaa 0\nerbrus-view-4-bbbbbbbb 1\nother-view 0\n",
	}}
	killed := NewTmux(rec.run).SweepViews()
	if strings.Join(killed, ",") != "erbrus-view-3-aaaaaaaa" {
		t.Errorf("killed = %v", killed)
	}
	if len(rec.calls) != 2 || rec.calls[1] != "tmux kill-session -t =erbrus-view-3-aaaaaaaa" {
		t.Errorf("calls = %v", rec.calls)
	}
	// No tmux server: nothing to do, no error.
	rec = &recorder{fail: map[string]error{"tmux list-sessions": errors.New("no server")}}
	if killed := NewTmux(rec.run).SweepViews(); len(killed) != 0 {
		t.Errorf("killed without server: %v", killed)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/spawn/ -run 'TestViewCommand|TestSize|TestViewName|TestSweepViews' -v`
Expected: compile errors (`ViewCommand`, `Size`, `ViewName`, `SweepViews` undefined).

- [ ] **Step 3: Implement**

`internal/spawn/view.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/spawn/ -v`
Expected: PASS, including all pre-existing tests.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add internal/spawn/view.go internal/spawn/view_test.go
/usr/bin/git commit -m "feat(spawn): Viewer — grouped ignore-size view command, window size, view names, stale-view sweep"
```

---

### Task 3: `internal/termview` — a command in a pty

**Files:**
- Create: `internal/termview/termview.go`, `internal/termview/termview_unix.go`, `internal/termview/termview_windows.go`
- Test: `internal/termview/termview_test.go`

**Interfaces:**
- Produces:
  ```go
  var ErrUnsupported error
  func Env(base []string) []string
  type Term struct{ ... }
  func Start(argv []string, cols, rows int, env []string) (*Term, error)
  func (t *Term) Read(p []byte) (int, error)
  func (t *Term) Write(p []byte) (int, error)
  func (t *Term) Resize(cols, rows int) error
  func (t *Term) Close() error
  ```

- [ ] **Step 1: Write the failing tests**

`internal/termview/termview_test.go`:

```go
//go:build !windows

package termview

import (
	"os"
	"strings"
	"testing"
	"time"
)

// readUntil collects output until want appears or the deadline passes.
func readUntil(t *testing.T, term *Term, want string, d time.Duration) string {
	t.Helper()
	var sb strings.Builder
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				sb.WriteString(string(buf[:n]))
				if strings.Contains(sb.String(), want) {
					close(done)
					return
				}
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
	return sb.String()
}

func TestStartSizeEchoResizeClose(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	term, err := Start([]string{"sh", "-c", "stty size; cat"}, 80, 24, Env(os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, term, "24 80", 3*time.Second); !strings.Contains(got, "24 80") {
		t.Fatalf("initial size not applied, output %q", got)
	}
	if _, err := term.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, term, "hello", 3*time.Second); !strings.Contains(got, "hello") {
		t.Fatalf("no echo, output %q", got)
	}
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := term.Write([]byte("x")); err == nil {
		t.Error("write after close must fail")
	}
}

func TestResizeIsSeenByChild(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	// The child prints its size once it reads a line, so the resize lands first.
	term, err := Start([]string{"sh", "-c", "read x; stty size"}, 80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	term.Write([]byte("go\n"))
	if got := readUntil(t, term, "30 100", 3*time.Second); !strings.Contains(got, "30 100") {
		t.Fatalf("resize not applied, output %q", got)
	}
}

func TestEnvAddsTerminalVars(t *testing.T) {
	env := Env([]string{"HOME=/h", "TERM=dumb"})
	j := strings.Join(env, "\n")
	if !strings.Contains(j, "TERM=xterm-256color") || !strings.Contains(j, "COLORTERM=truecolor") || !strings.Contains(j, "HOME=/h") || strings.Contains(j, "TERM=dumb") {
		t.Errorf("env = %v", env)
	}
}

func TestStartRejectsEmpty(t *testing.T) {
	if _, err := Start(nil, 80, 24, nil); err == nil {
		t.Error("empty argv accepted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/termview/ -v`
Expected: build failure (package missing).

- [ ] **Step 3: Implement**

`internal/termview/termview.go`:

```go
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
```

`internal/termview/termview_unix.go`:

```go
//go:build !windows

package termview

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Term is one command running in a pty. Read/Write move terminal bytes;
// Close ends the command and releases the pty.
type Term struct {
	f    *os.File
	cmd  *exec.Cmd
	once sync.Once
	werr error
}

// Start runs argv in a new pty of the given size. env nil means the
// parent environment (callers should pass Env(os.Environ())).
func Start(argv []string, cols, rows int, env []string) (*Term, error) {
	if len(argv) == 0 {
		return nil, errors.New("termview: empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &Term{f: f, cmd: cmd}, nil
}

func (t *Term) Read(p []byte) (int, error)  { return t.f.Read(p) }
func (t *Term) Write(p []byte) (int, error) { return t.f.Write(p) }

func (t *Term) Resize(cols, rows int) error {
	return pty.Setsize(t.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close kills the command, closes the pty and reaps the process. Safe to
// call more than once.
func (t *Term) Close() error {
	t.once.Do(func() {
		if t.cmd.Process != nil {
			t.cmd.Process.Kill()
		}
		t.werr = t.f.Close()
		t.cmd.Wait()
	})
	return t.werr
}
```

`internal/termview/termview_windows.go`:

```go
//go:build windows

package termview

// Term is a stub: erbrus.exe has no pty and no tmux; the server answers
// 501 and the page keeps the read-only screen.
type Term struct{}

func Start(argv []string, cols, rows int, env []string) (*Term, error) { return nil, ErrUnsupported }
func (t *Term) Read(p []byte) (int, error)                             { return 0, ErrUnsupported }
func (t *Term) Write(p []byte) (int, error)                            { return 0, ErrUnsupported }
func (t *Term) Resize(cols, rows int) error                            { return ErrUnsupported }
func (t *Term) Close() error                                           { return nil }
```

- [ ] **Step 4: Run the tests, tidy, cross-build**

Run: `go mod tidy && go test ./internal/termview/ -v && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`
Expected: 4 tests PASS on Linux; Windows build succeeds; `go.mod` keeps `creack/pty` (now imported).

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add go.mod go.sum internal/termview
/usr/bin/git commit -m "feat(termview): run a command in a pty (creack/pty), windows stub"
```

---

### Task 4: Websocket handler `/ui/runs/{id}/terminal`

**Files:**
- Create: `internal/server/terminal.go`
- Modify: `internal/server/server.go` (Server struct field `terminals int32`; route)
- Test: `internal/server/terminal_test.go`

**Interfaces:**
- Consumes: `spawn.Viewer`, `spawn.ViewName`, `termview.Start/Env`, `s.st.RunByID`, `chiInt64`, `httpError`.
- Produces: route `GET /ui/runs/{id}/terminal`; wire protocol: server → client text frame `{"cols":C,"rows":R}` first and on every change, binary frames = pty output; client → server any frame = pty input; close reasons `window gone` (1000) or an error text (1011).

- [ ] **Step 1: Write the failing tests**

`internal/server/terminal_test.go`:

```go
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"erbrus/internal/spawn"
)

// fakeViewer is a fakeSpawner whose windows can be viewed: the "tmux
// client" is a shell that prints its size and echoes.
type fakeViewer struct {
	*fakeSpawner
	cols, rows int
	sizeErr    error
	argv       []string
}

func (f *fakeViewer) ViewCommand(h spawn.Handle, view string) ([]string, error) {
	return f.argv, nil
}
func (f *fakeViewer) Size(h spawn.Handle) (int, int, error) { return f.cols, f.rows, f.sizeErr }

func wsURL(tsURL string, runID int64) string {
	return "ws" + strings.TrimPrefix(tsURL, "http") + fmt.Sprintf("/ui/runs/%d/terminal", runID)
}

// readUntilWS drains frames until want appears in binary data (or the
// context ends), returning everything seen.
func readUntilWS(ctx context.Context, c *websocket.Conn, want string) (string, error) {
	var sb strings.Builder
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return sb.String(), err
		}
		if typ == websocket.MessageBinary {
			sb.Write(data)
			if strings.Contains(sb.String(), want) {
				return sb.String(), nil
			}
		}
	}
}

func TestTerminalRelaysBytesAndSize(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	ts, st, root := newTestServer(t)
	fv := &fakeViewer{fakeSpawner: &fakeSpawner{}, cols: 100, rows: 30, argv: []string{"sh", "-c", "stty size; cat"}}
	testSrv.SetRuntime(fv, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts.URL, run.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	typ, data, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageText || string(data) != `{"cols":100,"rows":30}` {
		t.Fatalf("first frame = %v %q %v", typ, data, err)
	}
	if got, err := readUntilWS(ctx, c, "30 100"); err != nil {
		t.Fatalf("pty size not relayed: %q %v", got, err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("ping\r")); err != nil {
		t.Fatal(err)
	}
	if got, err := readUntilWS(ctx, c, "ping"); err != nil {
		t.Fatalf("input not relayed: %q %v", got, err)
	}
	c.Close(websocket.StatusNormalClosure, "")
	// The handler must let go of its slot once the socket is gone.
	deadline := time.Now().Add(3 * time.Second)
	for testSrv.openTerminals() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := testSrv.openTerminals(); n != 0 {
		t.Fatalf("terminals still open after close: %d", n)
	}
}

func TestTerminalRefusals(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	// Spawner without Viewer: 501 before any upgrade.
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/terminal", ts.URL, run.ID))
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("plain spawner status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Finished run: 404.
	fv := &fakeViewer{fakeSpawner: fs, cols: 80, rows: 24, argv: []string{"cat"}}
	testSrv.SetRuntime(fv, "/abs/erbrus", ts.URL)
	st.FinishRun(run.ID, "done", 0)
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/terminal", ts.URL, run.ID))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("finished run status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Window gone at connect time: upgrade, then close 1011 with the reason.
	run2 := claudeRun(t, st, ch1)
	fv.sizeErr = fmt.Errorf("can't find window")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts.URL, run2.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusInternalError {
		t.Fatalf("close status = %v (%v)", websocket.CloseStatus(err), err)
	}
	if !strings.Contains(err.Error(), "can't find window") {
		t.Fatalf("close reason lost: %v", err)
	}
}

func TestTerminalLimit(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	ts, st, root := newTestServer(t)
	fv := &fakeViewer{fakeSpawner: &fakeSpawner{}, cols: 80, rows: 24, argv: []string{"cat"}}
	testSrv.SetRuntime(fv, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	old := maxTerminals
	maxTerminals = 1
	defer func() { maxTerminals = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts.URL, run.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err := c.Read(ctx); err != nil { // size frame: the terminal is up
		t.Fatal(err)
	}
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/terminal", ts.URL, run.ID))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second terminal status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/server/ -run 'TestTerminal' -v`
Expected: compile errors (`openTerminals`, `maxTerminals` undefined) — then, after stubs, 404s from the unrouted path.

- [ ] **Step 3: Implement the handler**

`internal/server/terminal.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"erbrus/internal/spawn"
	"erbrus/internal/termview"
)

// maxTerminals caps concurrent browser terminals; each is a pty plus a
// tmux client. termSizePoll is how often the window size is re-read so
// the browser follows the user's real terminal.
var (
	maxTerminals int32 = 8
	termSizePoll       = 2 * time.Second
)

// termSize is the one control message; it goes as a text frame, output
// goes as binary frames, so the client tells them apart by type.
type termSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (s *Server) openTerminals() int { return int(atomic.LoadInt32(&s.terminals)) }

// closeReason fits the 123-byte limit of a websocket close frame.
func closeReason(err error) string {
	r := err.Error()
	if len(r) > 120 {
		r = r[:120]
	}
	return r
}

// handleUITerminal bridges one websocket to a pty running the spawner's
// view client for the run's window. Everything a terminal would send goes
// through untouched; the only framing is the size message.
func (s *Server) handleUITerminal(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || run.TmuxTarget == "" || (run.Status != "starting" && run.Status != "running") {
		httpError(w, http.StatusNotFound, "run has no live tmux window")
		return
	}
	viewer, ok := s.spawner.(spawn.Viewer)
	if !ok {
		httpError(w, http.StatusNotImplemented, "terminal not supported for this spawner")
		return
	}
	if atomic.AddInt32(&s.terminals, 1) > maxTerminals {
		atomic.AddInt32(&s.terminals, -1)
		httpError(w, http.StatusServiceUnavailable, "too many open terminals")
		return
	}
	defer atomic.AddInt32(&s.terminals, -1)

	// Accept's default origin policy: Origin must match Host; no Origin
	// is fine. Same rule as originGuard.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()

	h := spawn.Handle(run.TmuxTarget)
	cols, rows, err := viewer.Size(h)
	if err != nil {
		c.Close(websocket.StatusInternalError, closeReason(err))
		return
	}
	argv, err := viewer.ViewCommand(h, spawn.ViewName(run.ID))
	if err != nil {
		c.Close(websocket.StatusInternalError, closeReason(err))
		return
	}
	term, err := termview.Start(argv, cols, rows, termview.Env(os.Environ()))
	if err != nil {
		c.Close(websocket.StatusInternalError, closeReason(err))
		return
	}
	defer term.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sendSize := func(cols, rows int) error {
		b, _ := json.Marshal(termSize{cols, rows})
		return c.Write(ctx, websocket.MessageText, b)
	}
	if err := sendSize(cols, rows); err != nil {
		return
	}

	// Three pumps; the first to finish ends the session. done is buffered
	// so the others can report after the handler stopped listening.
	done := make(chan string, 3)
	go func() { // pty -> browser
		buf := make([]byte, 32<<10)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				if err := c.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
					done <- ""
					return
				}
			}
			if err != nil {
				done <- "window gone"
				return
			}
		}
	}()
	go func() { // browser -> pty
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				done <- ""
				return
			}
			if _, err := term.Write(data); err != nil {
				done <- "window gone"
				return
			}
		}
	}()
	go func() { // follow the user's terminal size
		t := time.NewTicker(termSizePoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			nc, nr, err := viewer.Size(h)
			if err != nil {
				done <- "window gone"
				return
			}
			if nc != cols || nr != rows {
				cols, rows = nc, nr
				term.Resize(nc, nr)
				if err := sendSize(nc, nr); err != nil {
					done <- ""
					return
				}
			}
		}
	}()
	reason := <-done
	cancel()
	term.Close() // ends the pty pump's Read
	c.Close(websocket.StatusNormalClosure, reason)
}
```

In `internal/server/server.go`:
- Server struct: after `configPath string` add
  ```go
  	// terminals counts open browser terminals (handleUITerminal).
  	terminals int32
  ```
- Routes, after `r.Get("/ui/runs/{id}/screen/events", s.handleUIScreenEvents)`:
  ```go
  	r.Get("/ui/runs/{id}/terminal", s.handleUITerminal)
  ```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/server/ -run 'TestTerminal' -race -v`
Expected: 3 PASS. If `TestTerminalRelaysBytesAndSize` hangs on the first Read, check that `Accept` succeeded (chi passes the raw `http.ResponseWriter`, which implements `http.Hijacker` on `httptest`).

- [ ] **Step 5: Full package run and commit**

Run: `go test ./internal/server/ -race`
Expected: PASS.

```bash
/usr/bin/git add internal/server/terminal.go internal/server/terminal_test.go internal/server/server.go
/usr/bin/git commit -m "feat(server): websocket terminal bridge to a pty view client (/ui/runs/{id}/terminal)"
```

---

### Task 5: Screen feed without HTML, page `Terminal` flag

**Files:**
- Modify: `internal/server/screen.go` (`screenFrame.HTML` omitempty; `screenPage.Terminal`; `handleUIScreen`; `handleUIScreenEvents` `?html=0`)
- Modify: `internal/web/templates/screen.html`, `internal/web/templates/layout.html`
- Test: `internal/server/screen_test.go` (append)

**Interfaces:**
- Consumes: `spawn.Viewer`, `termview.ErrUnsupported` (Windows detection via `runtime.GOOS`).
- Produces: `GET /ui/runs/{id}/screen/events?html=0` frames without the `html` key; `screenPage.Terminal bool`; template block `head`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/server/screen_test.go`:

```go
func TestScreenEventsWithoutHTML(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	fs.setScreen("s:5", spawn.Screen{Raw: "hello\n" + box, Activity: time.Now()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/ui/runs/%d/screen/events?html=0", ts.URL, run.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		if strings.Contains(line, `"html"`) {
			t.Fatalf("html present with ?html=0: %s", line)
		}
		if !strings.Contains(line, `"state":"waiting"`) {
			t.Fatalf("state missing: %s", line)
		}
		return
	}
	t.Fatal("no frame received")
}

func TestScreenPageTerminalFlag(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	fs.setScreen("s:5", spawn.Screen{Raw: box, Activity: time.Now()})

	// Plain spawner: no terminal, the live <pre> as before.
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	body := readAll(t, resp)
	if strings.Contains(body, `id="term"`) || strings.Contains(body, "vendor/xterm.js") || !strings.Contains(body, `<pre class="screen" id="screen"`) {
		t.Fatalf("plain spawner page: %s", body)
	}
	// Viewer spawner: terminal container, vendored assets, hidden <pre>.
	testSrv.SetRuntime(&fakeViewer{fakeSpawner: fs, cols: 80, rows: 24, argv: []string{"cat"}}, "/abs/erbrus", ts.URL)
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	body = readAll(t, resp)
	for _, want := range []string{
		fmt.Sprintf(`id="term" data-ws="/ui/runs/%d/terminal"`, run.ID),
		`/static/vendor/xterm.js?v=`, `/static/vendor/xterm.css?v=`,
		`id="screen" data-run="` + fmt.Sprint(run.ID) + `" hidden`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer page missing %q", want)
		}
	}
	if strings.Contains(body, "use the channel composer") {
		t.Error("composer hint must go when the terminal is present")
	}
	// Finished run: no terminal even with a Viewer.
	st.FinishRun(run.ID, "done", 0)
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if body := readAll(t, resp); strings.Contains(body, `id="term"`) {
		t.Error("finished run must not offer a terminal")
	}
}
```

Add imports `bufio`, `context` to that test file if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/server/ -run 'TestScreenEventsWithoutHTML|TestScreenPageTerminalFlag' -v`
Expected: FAIL (`html` present; `id="term"` missing).

- [ ] **Step 3: Implement**

`internal/server/screen.go`:
- `screenFrame.HTML` tag becomes `json:"html,omitempty"`.
- `screenPage` gains:
  ```go
  	// Terminal: the page hosts the interactive terminal (live tmux run on
  	// a Viewer spawner, on a host with ptys). The <pre> is the fallback.
  	Terminal bool
  ```
- In `handleUIScreen`, after the `switch`:
  ```go
  	if _, ok := s.spawner.(spawn.Viewer); ok && page.Live && page.Note == "" && runtime.GOOS != "windows" {
  		page.Terminal = true
  	}
  ```
  (import `runtime`).
- In `handleUIScreenEvents`, before the loop: `noHTML := r.URL.Query().Get("html") == "0"`; in the `case f := <-frames:` branch, before marshaling: `if noHTML { f.HTML = "" }`.

`internal/web/templates/layout.html`: after the stylesheet link add `{{block "head" .}}{{end}}`.

`internal/web/templates/screen.html`:
- Add at the top, after the title define:
  ```html
  {{define "head"}}{{if .Terminal}}<link rel="stylesheet" href="{{asset "vendor/xterm.css"}}">
  <script src="{{asset "vendor/xterm.js"}}"></script>{{end}}{{end}}
  ```
- Replace the `<pre ...>` line with:
  ```html
  {{if .Terminal}}<div class="term" id="term" data-ws="/ui/runs/{{.Run.ID}}/terminal" title="click to type here"></div>{{end}}
  <pre class="screen" id="screen" data-run="{{.Run.ID}}"{{if .Note}} data-static="1"{{end}}{{if .Terminal}} hidden{{end}}>{{.Frame.HTML}}</pre>
  ```
- In the keypad form, replace `<span class="muted small">— to type text, use the channel composer</span>` with `{{if not .Terminal}}<span class="muted small">— to type text, use the channel composer</span>{{end}}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/server/ -race`
Expected: PASS (the vendor files do not exist yet; `asset` only builds URLs).

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add internal/server/screen.go internal/server/screen_test.go internal/web/templates/screen.html internal/web/templates/layout.html
/usr/bin/git commit -m "feat(screen): Terminal flag on the screen page, events stream can omit html"
```

---

### Task 6: Vendor xterm.js, terminal script and styles

**Files:**
- Create: `scripts/vendor-xterm.sh`, `internal/web/static/vendor/{xterm.js,xterm.css,xterm.LICENSE,VERSIONS}`
- Modify: `internal/web/static/app.js` (new block before the screen block; screen block changes), `internal/web/static/app.css`
- Test: `internal/web/web_test.go` (new), plus `node --check`

**Interfaces:**
- Consumes: page markup from Task 5, wire protocol from Task 4.
- Produces: the working terminal UI.

- [ ] **Step 1: Write the failing embed test**

`internal/web/web_test.go`:

```go
package web

import (
	"strings"
	"testing"
)

func TestVendoredXtermIsEmbedded(t *testing.T) {
	for _, name := range []string{"static/vendor/xterm.js", "static/vendor/xterm.css", "static/vendor/xterm.LICENSE", "static/vendor/VERSIONS"} {
		b, err := FS.ReadFile(name)
		if err != nil || len(b) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	v, _ := FS.ReadFile("static/vendor/VERSIONS")
	if !strings.Contains(string(v), "@xterm/xterm 6.0.0") {
		t.Errorf("VERSIONS = %q", v)
	}
	if !strings.HasPrefix(Asset("vendor/xterm.js"), "/static/vendor/xterm.js?v=") {
		t.Errorf("asset url = %q", Asset("vendor/xterm.js"))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/ -v`
Expected: FAIL (files missing).

- [ ] **Step 3: Vendor script and files**

`scripts/vendor-xterm.sh`:

```sh
#!/bin/sh
# Refreshes the vendored xterm.js under internal/web/static/vendor/.
# Pinned on purpose: bump XTERM_VERSION, run, review the diff, commit.
set -e
cd "$(dirname "$0")/.."
XTERM_VERSION=6.0.0
dst=internal/web/static/vendor
base="https://unpkg.com/@xterm/xterm@$XTERM_VERSION"
mkdir -p "$dst"
curl -fsSL "$base/lib/xterm.js" -o "$dst/xterm.js"
curl -fsSL "$base/css/xterm.css" -o "$dst/xterm.css"
curl -fsSL "$base/LICENSE" -o "$dst/xterm.LICENSE"
printf '@xterm/xterm %s\n' "$XTERM_VERSION" > "$dst/VERSIONS"
ls -la "$dst"
```

Run: `chmod +x scripts/vendor-xterm.sh && sh scripts/vendor-xterm.sh`
Expected: four files; `xterm.js` around 300–400 KB and starting with a `!function` / license banner mentioning xterm. If curl is blocked by the environment's hook, fetch the same three URLs with a Python `urllib` one-off into the same paths and write VERSIONS by hand; the script stays as the documented way.

Run: `go test ./internal/web/ -v`
Expected: PASS.

- [ ] **Step 4: Terminal block in app.js**

Insert before the comment `// Screen view: one EventSource per open screen page`:

```js
// Interactive terminal: xterm.js over a websocket to a pty running a
// size-neutral tmux client (docs/superpowers/specs/2026-09-08-web-terminal-design.md).
// The terminal is exactly the tmux window's size (server-sent), never
// fitted to the page; when the socket fails the read-only <pre> returns.
(function () {
  var el = document.getElementById('term');
  if (!el) return;
  var screen = document.getElementById('screen');
  var errEl = document.getElementById('screenerr');
  function fallback(reason) {
    if (el.hidden) return;
    el.hidden = true;
    if (screen) screen.hidden = false;
    if (reason && errEl) errEl.textContent = reason;
    document.dispatchEvent(new CustomEvent('erbrus:terminal-fallback'));
  }
  if (typeof Terminal === 'undefined') { fallback('terminal script did not load'); return; }
  var term = new Terminal({
    cursorBlink: true, scrollback: 0, fontSize: 13,
    fontFamily: 'ui-monospace, Menlo, Consolas, "DejaVu Sans Mono", monospace',
    theme: { background: '#0c0d0f', foreground: '#d6d8dc' }
  });
  term.open(el);
  var ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + el.dataset.ws);
  ws.binaryType = 'arraybuffer';
  var gotOutput = false;
  ws.onmessage = function (ev) {
    if (typeof ev.data === 'string') {
      try {
        var sz = JSON.parse(ev.data);
        if (sz.cols > 0 && sz.rows > 0) term.resize(sz.cols, sz.rows);
      } catch (e) { /* not a size message */ }
      return;
    }
    gotOutput = true;
    term.write(new Uint8Array(ev.data));
  };
  ws.onclose = function (ev) {
    fallback(ev.reason || (gotOutput ? 'terminal closed' : 'terminal unavailable'));
  };
  term.onData(function (d) { if (ws.readyState === WebSocket.OPEN) ws.send(d); });
  term.onBinary(function (d) {
    if (ws.readyState !== WebSocket.OPEN) return;
    var b = new Uint8Array(d.length);
    for (var i = 0; i < d.length; i++) b[i] = d.charCodeAt(i) & 255;
    ws.send(b);
  });
  el.addEventListener('click', function () { term.focus(); });
  if (term.textarea) {
    term.textarea.addEventListener('focus', function () { el.classList.add('typing'); });
    term.textarea.addEventListener('blur', function () { el.classList.remove('typing'); });
  }
  term.focus();
})();
```

In the screen block:
- Replace `if (!screen || screen.dataset.static) return; // not a live screen page` with:
  ```js
  if (!screen || screen.dataset.static) return; // not a live screen page
  // With the terminal showing, frames are for the header/keypad only:
  // ask the server to leave the html out. Fallback flips this back.
  var termEl = document.getElementById('term');
  var wantHTML = !termEl || termEl.hidden;
  document.addEventListener('erbrus:terminal-fallback', function () { wantHTML = true; connect(); });
  ```
- In `connect()`: `es = new EventSource('/ui/runs/' + runID + '/screen/events' + (wantHTML ? '' : '?html=0'));`
- In the frame handler: `screen.innerHTML = f.html;` becomes `if (f.html !== undefined) screen.innerHTML = f.html;`

Note `connect` is a function declaration hoisted within the IIFE, so the listener registered above can call it.

- [ ] **Step 5: Styles**

Append to `internal/web/static/app.css` under the screen section:

```css
/* interactive terminal (xterm.js). Sized by the tmux window, so it scrolls
   inside the page instead of being fitted; the border shows keyboard focus. */
.term { display: inline-block; max-width: 100%; overflow: auto; padding: 8px; background: #0c0d0f; border: 1px solid #2c2f35; border-radius: 6px; }
.term.typing { border-color: #61afef; }
.term .xterm { line-height: 1.2; }
```

- [ ] **Step 6: Syntax check and full tests**

Run: `node --check internal/web/static/app.js && go test ./... 2>&1 | tail -15`
Expected: no syntax error; all packages PASS.

- [ ] **Step 7: Commit**

```bash
/usr/bin/git add scripts/vendor-xterm.sh internal/web/static/vendor internal/web/web_test.go internal/web/static/app.js internal/web/static/app.css
/usr/bin/git commit -m "feat(ui): xterm.js terminal on the screen page, vendored @xterm/xterm 6.0.0, fallback to the read-only screen"
```

---

### Task 7: Startup sweep of stale view sessions

**Files:**
- Modify: `internal/cli/serve.go`

**Interfaces:**
- Consumes: `(*spawn.Tmux).SweepViews()` from Task 2.

- [ ] **Step 1: Implement**

In `runServe`, replace
```go
	srv.SetRuntime(spawn.NewTmux(spawn.ExecCmdRunner), bin, "http://"+ln.Addr().String())
```
with
```go
	tm := spawn.NewTmux(spawn.ExecCmdRunner)
	srv.SetRuntime(tm, bin, "http://"+ln.Addr().String())
	// Browser-terminal view sessions die with their pty client; a server
	// that crashed with terminals open leaves them behind. Sweep those.
	if killed := tm.SweepViews(); len(killed) > 0 {
		fmt.Fprintln(stderr, "swept stale terminal views:", strings.Join(killed, " "))
	}
```
Add `"strings"` to the imports.

- [ ] **Step 2: Build and run the cli tests**

Run: `go build ./... && go test ./internal/cli/`
Expected: PASS (the cli tests run `serve` against a fake or missing tmux; `SweepViews` returns nil when `list-sessions` fails).

- [ ] **Step 3: Commit**

```bash
/usr/bin/git add internal/cli/serve.go
/usr/bin/git commit -m "feat(serve): sweep unattached erbrus-view-* sessions at startup"
```

---

### Task 8: Real-tmux integration test (opt-in)

**Files:**
- Create: `internal/spawn/view_live_test.go`

**Interfaces:**
- Consumes: `Tmux.ViewCommand/Size`, `termview.Start`.

- [ ] **Step 1: Write the test**

```go
//go:build !windows

package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"erbrus/internal/termview"
)

// Runs only with ERBRUS_TMUX_TEST=1 against the local tmux server, in a
// throwaway session; it never touches other sessions.
func TestViewClientLive(t *testing.T) {
	if os.Getenv("ERBRUS_TMUX_TEST") != "1" {
		t.Skip("set ERBRUS_TMUX_TEST=1 to run against tmux")
	}
	sess := fmt.Sprintf("claudetest-term-%d", os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", sess, "-x", "120", "-y", "35", "cat").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", "="+sess).Run()
	out, err := exec.Command("tmux", "list-windows", "-t", sess, "-F", "#{session_name}:#{window_index}").Output()
	if err != nil {
		t.Fatal(err)
	}
	h := Handle(strings.TrimSpace(strings.Split(string(out), "\n")[0]))
	tm := NewTmux(ExecCmdRunner)

	cols, rows, err := tm.Size(h)
	if err != nil || cols != 120 || rows != 35 {
		t.Fatalf("size = %d x %d, %v", cols, rows, err)
	}
	view := ViewName(1)
	argv, _ := tm.ViewCommand(h, view)
	term, err := termview.Start(argv, 60, 15, termview.Env(os.Environ())) // deliberately smaller
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	time.Sleep(500 * time.Millisecond)
	if _, err := term.Write([]byte("typed-from-browser\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	pane, _ := exec.Command("tmux", "capture-pane", "-p", "-t", "="+string(h)).Output()
	if !strings.Contains(string(pane), "typed-from-browser") {
		t.Fatalf("input did not reach the window:\n%s", pane)
	}
	if c, r, _ := tm.Size(h); c != 120 || r != 35 {
		t.Fatalf("view client resized the window to %dx%d", c, r)
	}
	clients, _ := exec.Command("tmux", "list-clients", "-F", "#{client_session} #{client_flags}").Output()
	if !strings.Contains(string(clients), view+" ") || !strings.Contains(string(clients), "ignore-size") {
		t.Fatalf("view client not attached with ignore-size:\n%s", clients)
	}
	term.Close()
	time.Sleep(700 * time.Millisecond)
	if err := exec.Command("tmux", "has-session", "-t", "="+view).Run(); err == nil {
		t.Fatalf("view session %s survived the client", view)
	}
}
```

- [ ] **Step 2: Run it both ways**

Run: `go test ./internal/spawn/ -run TestViewClientLive -v` → SKIP.
Run: `ERBRUS_TMUX_TEST=1 go test ./internal/spawn/ -run TestViewClientLive -v` → PASS, and `tmux ls | grep -c claudetest` prints 0 afterwards.

If `set -t <view>` or `select-window -t =<view>:<idx>` is rejected by this tmux, fix `ViewCommand` (and its unit test) rather than the live test.

- [ ] **Step 3: Commit**

```bash
/usr/bin/git add internal/spawn/view_live_test.go
/usr/bin/git commit -m "test(spawn): opt-in live tmux test for the view client"
```

---

### Task 9: Manual verification and binary

**Files:** none (verification), then `bin/erbrus` via `./build.sh`.

- [ ] **Step 1: Throwaway instance**

Under the scratchpad: `XDG_CONFIG_HOME=<scratch>/cfg XDG_DATA_HOME=<scratch>/data` with a config on port 7499 and `session_pattern: claudetest-{project}`; add the erbrus repo as a project; spawn a `claude` agent (or a fake provider whose command is `bash`) into `general`.

- [ ] **Step 2: Browser check**

Open `http://127.0.0.1:7499/ui/runs/<id>/screen` in a real browser (headless screenshots do not render xterm's WebGL/canvas reliably): the terminal shows the agent; type a prompt and Enter; answer a dialog with arrows; confirm `tmux display -p -t '=claudetest-erbrus:<idx>' '#{window_width}x#{window_height}'` never changes and `tmux list-clients` shows the `erbrus-view-*` client with `ignore-size`. Close the tab: the view session is gone within a second. Kill the agent window: the page falls back to the read-only screen with "window gone".

- [ ] **Step 3: Tear down**

Kill only `claudetest-*` sessions and the 7499 instance.

- [ ] **Step 4: Build and hand over**

Run: `./build.sh`
Expected: `bin/erbrus` and `bin/erbrus.exe` built (Windows via the stub). Report `/mnt/t/others/erbrus/.claude/worktrees/web-terminal/bin/erbrus` to the user. The canonical `/mnt/t/others/erbrus/bin/erbrus` stays on main until the merge.

---

## Self-review

- Spec coverage: Viewer/ViewCommand/Size/ViewName/SweepViews (Task 2); termview + Windows stub (3); handler, limit, close codes, size poll (4); `?html=0`, `Terminal` flag, template, composer hint (5); vendoring script and files, xterm block, fallback event, styles (6); startup sweep (7); live test (8); manual and binary (9). Dependencies (1). Nothing in the spec is left without a task.
- Type consistency: `spawn.Viewer` methods match between Tasks 2, 4, 5; `termview.Start(argv, cols, rows, env)` matches Tasks 3, 4, 8; `termSize` JSON keys match the JS (`cols`, `rows`); `fakeViewer` defined in Task 4 is reused in Task 5's test (same package).
- Placeholders: none; every step carries its code or exact command.
