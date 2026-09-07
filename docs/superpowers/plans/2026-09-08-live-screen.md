# Live Screen View (read-only) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "Screen" page per run that shows the agent's rendered tmux pane live in the web UI, colors preserved, with a last-activity indicator.

**Architecture:** `Spawner` gains `Capture` (tmux `capture-pane -p -e` + `display -p` format vars). A server-side `screenFeed` polls `Capture` only for runs that have an open viewer and fans out frames over a per-run SSE endpoint, never through the global `Hub`. A whitelist ANSI→HTML converter renders the frame; the page swaps `innerHTML`.

**Tech Stack:** Go 1.26, existing deps only (chi, modernc sqlite, goldmark, yaml.v3). Zero new dependencies. tmux ≥ 3.x (`window_activity`, `pane_dead` verified on 3.7c).

**Spec:** /mnt/t/others/erbrus/docs/superpowers/specs/2026-09-08-live-screen-design.md

## Global Constraints

- Develop directly on `main` in /mnt/t/others/erbrus, commit per task, never push.
- Every tmux target goes through `exact()` (`=` prefix) — see the comment in `internal/spawn/tmux.go`.
- Frames must not be published through `Hub` (16-deep, drop-on-full, shared with chat events).
- No `template.HTML` around user content except the converter's output, which escapes all text itself.
- After the last task: `go test -race ./...`, `./build.sh`, hand over `/mnt/t/others/erbrus/bin/erbrus`.

---

### Task 1: `Spawner.Capture` (spawn package)

**Files:**
- Modify: `internal/spawn/spawn.go` (Screen type, interface method)
- Modify: `internal/spawn/tmux.go` (Tmux.Capture)
- Modify: `internal/server/runs_test.go:18-39` (fakeSpawner gains Capture)
- Test: `internal/spawn/tmux_test.go`

**Interfaces:**
- Produces: `type Screen struct { Raw string; Dead bool; Activity time.Time; Cols, Rows int }` and `Capture(h Handle) (Screen, error)` on `Spawner`.

- [ ] **Step 1: Write the failing tests** (append to `internal/spawn/tmux_test.go`)

```go
func TestCaptureIssuesExactTargetsAndParses(t *testing.T) {
	rec := &recorder{out: map[string]string{
		"tmux capture-pane": "\x1b[1mhello\x1b[0m\nworld\n",
		"tmux display":      "0 1788820410 134 36\n",
	}}
	sc, err := NewTmux(rec.run).Capture("erbrus-webshop:1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux capture-pane -p -e -t =erbrus-webshop:1",
		"tmux display -p -t =erbrus-webshop:1 #{pane_dead} #{window_activity} #{pane_width} #{pane_height}",
	}
	if strings.Join(rec.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(rec.calls, "\n"), strings.Join(want, "\n"))
	}
	if sc.Raw != "\x1b[1mhello\x1b[0m\nworld\n" {
		t.Errorf("raw = %q", sc.Raw)
	}
	if sc.Dead {
		t.Error("pane reported dead")
	}
	if sc.Activity.Unix() != 1788820410 {
		t.Errorf("activity = %v", sc.Activity)
	}
	if sc.Cols != 134 || sc.Rows != 36 {
		t.Errorf("size = %dx%d", sc.Cols, sc.Rows)
	}
}

func TestCaptureDeadPane(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux display": "1 0 80 24\n"}}
	sc, err := NewTmux(rec.run).Capture("s:2")
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Dead || !sc.Activity.IsZero() {
		t.Errorf("dead=%v activity=%v", sc.Dead, sc.Activity)
	}
}

func TestCaptureWindowGone(t *testing.T) {
	rec := &recorder{fail: map[string]error{"tmux capture-pane": errors.New("can't find window")}}
	if _, err := NewTmux(rec.run).Capture("s:9"); err == nil {
		t.Fatal("expected error when the window is gone")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/spawn/ -run TestCapture`
Expected: compile error, `Capture` undefined.

- [ ] **Step 3: Implement**

In `internal/spawn/spawn.go`, add `"time"` to imports and:

```go
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
```

and in the `Spawner` interface after `Send`:

```go
	// Capture snapshots the run's visible screen. Errors when the window
	// is gone; a dead-but-retained pane still captures (Dead=true).
	Capture(h Handle) (Screen, error)
```

In `internal/spawn/tmux.go`, add `"strconv"` to imports and:

```go
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
```

In `internal/server/runs_test.go`, extend `fakeSpawner`:

```go
	// screens scripts Capture per handle; captureErr wins when set.
	// capMu guards them: the screen feed polls from a goroutine.
	capMu      sync.Mutex
	screens    map[spawn.Handle]spawn.Screen
	captureErr error
	captures   int
```

```go
func (f *fakeSpawner) Capture(h spawn.Handle) (spawn.Screen, error) {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	f.captures++
	if f.captureErr != nil {
		return spawn.Screen{}, f.captureErr
	}
	return f.screens[h], nil
}

// setScreen swaps the scripted screen for h (safe to call while polled).
func (f *fakeSpawner) setScreen(h spawn.Handle, sc spawn.Screen) {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	if f.screens == nil {
		f.screens = map[spawn.Handle]spawn.Screen{}
	}
	f.screens[h] = sc
}
```

(add `"sync"` to that file's imports).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/spawn/ ./internal/server/`
Expected: PASS (server package compiles again with the fake's new method).

- [ ] **Step 5: Commit**

```bash
git add internal/spawn/spawn.go internal/spawn/tmux.go internal/spawn/tmux_test.go internal/server/runs_test.go
git commit -m "feat(spawn): Capture snapshots a run's rendered pane + activity"
```

---

### Task 2: ANSI → HTML converter

**Files:**
- Create: `internal/server/ansi.go`
- Test: `internal/server/ansi_test.go`

**Interfaces:**
- Produces: `func ansiToHTML(raw string) template.HTML` — pure; escapes all text; SGR whitelist only.

- [ ] **Step 1: Write the failing tests** (`internal/server/ansi_test.go`)

```go
package server

import (
	"strings"
	"testing"
)

func TestANSIPlainTextEscaped(t *testing.T) {
	got := string(ansiToHTML("a <b> & c\nline2 <script>x</script>"))
	want := "a &lt;b&gt; &amp; c\nline2 &lt;script&gt;x&lt;/script&gt;"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestANSIBoldAndReset(t *testing.T) {
	got := string(ansiToHTML("\x1b[1mhi\x1b[0m there"))
	want := `<span style="font-weight:bold">hi</span> there`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestANSIColors(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mx\x1b[39m":      `<span style="color:#e0685a">x</span>`,
		"\x1b[38;5;114mx\x1b[0m": `<span style="color:#87d787">x</span>`,
		"\x1b[38;2;1;2;3mx":      `<span style="color:#010203">x</span>`,
		"\x1b[48;5;232mx":        `<span style="background:#080808">x</span>`,
		"\x1b[1;31mx":            `<span style="color:#e0685a;font-weight:bold">x</span>`,
		"\x1b[38:5:114mx":        `<span style="color:#87d787">x</span>`, // colon form
		"\x1b[7mx":               `<span style="color:#0c0d0f;background:#d6d8dc">x</span>`,
		"\x1b[mx":                `x`, // empty SGR = reset
	}
	for in, want := range cases {
		if got := string(ansiToHTML(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestANSIDropsUnknownSequences(t *testing.T) {
	got := string(ansiToHTML("\x1b[2Ja\x1b]0;title\x07b\x1b[?25lc\x1b(Bd\x1b[Kx\x1b]8;;http://x\x1b\\y"))
	if got != "abcdxy" {
		t.Errorf("got %q", got)
	}
}

func TestANSIStyleSpansCloseAtNewline(t *testing.T) {
	// A styled run spanning lines stays one span (pre keeps the newline).
	got := string(ansiToHTML("\x1b[32ma\nb\x1b[0m"))
	want := "<span style=\"color:#67c98a\">a\nb</span>"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Line observed in a live Claude Code window, 2026-09-08.
const claudeFixture = "\x1b[38;5;174m✻\x1b[39m \x1b[38;5;174mPrecipitating… \x1b[38;5;246m(7m 5s · ↓\x1b[39m \x1b[38;5;246m5.2k tokens)\x1b[39m\n" +
	"\x1b[38;5;244m────────────────\x1b[39m\n\x1b[38;5;246m❯ \x1b[39m\n"

func TestANSIClaudeCodeFixture(t *testing.T) {
	got := string(ansiToHTML(claudeFixture))
	if strings.Contains(got, "\x1b") {
		t.Error("escape byte leaked")
	}
	for _, want := range []string{"Precipitating…", "5.2k tokens)", "❯"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run TestANSI`
Expected: compile error, `ansiToHTML` undefined.

- [ ] **Step 3: Implement** (`internal/server/ansi.go`)

```go
package server

import (
	"fmt"
	"html"
	"html/template"
	"strconv"
	"strings"
)

// ansiToHTML renders a `tmux capture-pane -e` screen as safe HTML for a
// <pre>: SGR styling becomes inline-styled spans, every other escape
// sequence (cursor moves, OSC titles, charset switches) is dropped, and all
// text is HTML-escaped. Whitelist by construction: nothing from the input
// ever lands in an attribute — styles are built only from the fixed
// palette and %02x-formatted numbers.
func ansiToHTML(raw string) template.HTML {
	var out strings.Builder
	var text strings.Builder
	var st sgrState
	open := false // a <span> for st is open

	flush := func() {
		if text.Len() == 0 {
			return
		}
		out.WriteString(html.EscapeString(text.String()))
		text.Reset()
	}
	restyle := func(next sgrState) {
		if next == st {
			return
		}
		flush()
		if open {
			out.WriteString("</span>")
			open = false
		}
		st = next
		if s := st.style(); s != "" {
			out.WriteString(`<span style="` + s + `">`)
			open = true
		}
	}

	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != 0x1b {
			text.WriteByte(c)
			continue
		}
		if i+1 >= len(raw) {
			break
		}
		switch raw[i+1] {
		case '[': // CSI: params, then a final byte 0x40..0x7E
			j := i + 2
			for j < len(raw) && (raw[j] < 0x40 || raw[j] > 0x7e) {
				j++
			}
			if j >= len(raw) {
				i = len(raw)
				break
			}
			if raw[j] == 'm' {
				next := st
				applySGR(&next, raw[i+2:j])
				restyle(next)
			}
			i = j
		case ']': // OSC: until BEL or ESC \
			j := i + 2
			for j < len(raw) && raw[j] != 0x07 && !(raw[j] == 0x1b && j+1 < len(raw) && raw[j+1] == '\\') {
				j++
			}
			if j < len(raw) && raw[j] == 0x1b {
				j++
			}
			i = j
		default: // two-byte escape (ESC ( B, ESC 7, ...)
			i++
		}
	}
	flush()
	if open {
		out.WriteString("</span>")
	}
	return template.HTML(out.String())
}

type sgrState struct {
	fg, bg                                string // "#rrggbb" or ""
	bold, dim, italic, underline, reverse bool
}

// Default colors of .screen in app.css, used to realize reverse video.
const screenFg, screenBg = "#d6d8dc", "#0c0d0f"

func (s sgrState) style() string {
	fg, bg := s.fg, s.bg
	if s.reverse {
		if fg == "" {
			fg = screenFg
		}
		if bg == "" {
			bg = screenBg
		}
		fg, bg = bg, fg
	}
	var parts []string
	if fg != "" {
		parts = append(parts, "color:"+fg)
	}
	if bg != "" {
		parts = append(parts, "background:"+bg)
	}
	if s.bold {
		parts = append(parts, "font-weight:bold")
	}
	if s.dim {
		parts = append(parts, "opacity:.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

// applySGR folds one SGR parameter string ("1;31", "38;5;114", "38:2::r:g:b")
// into st. Unknown parameters are ignored; a malformed extended color
// ends processing of that sequence.
func applySGR(st *sgrState, params string) {
	fields := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	if len(fields) == 0 {
		*st = sgrState{}
		return
	}
	ps := make([]int, 0, len(fields))
	for _, f := range fields {
		n, _ := strconv.Atoi(f) // "" and junk read as 0
		ps = append(ps, n)
	}
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		switch {
		case p == 0:
			*st = sgrState{}
		case p == 1:
			st.bold = true
		case p == 2:
			st.dim = true
		case p == 3:
			st.italic = true
		case p == 4:
			st.underline = true
		case p == 7:
			st.reverse = true
		case p == 22:
			st.bold, st.dim = false, false
		case p == 23:
			st.italic = false
		case p == 24:
			st.underline = false
		case p == 27:
			st.reverse = false
		case p >= 30 && p <= 37:
			st.fg = palette16[p-30]
		case p == 39:
			st.fg = ""
		case p >= 40 && p <= 47:
			st.bg = palette16[p-40]
		case p == 49:
			st.bg = ""
		case p >= 90 && p <= 97:
			st.fg = palette16[p-90+8]
		case p >= 100 && p <= 107:
			st.bg = palette16[p-100+8]
		case p == 38 || p == 48:
			color, used := extendedColor(ps[i+1:])
			if used == 0 {
				return
			}
			if p == 38 {
				st.fg = color
			} else {
				st.bg = color
			}
			i += used
		}
	}
}

// extendedColor parses the tail of a 38/48 parameter: "5;n" (256-color) or
// "2;r;g;b" (truecolor). Returns the hex color and how many params it ate.
func extendedColor(rest []int) (string, int) {
	if len(rest) >= 2 && rest[0] == 5 {
		return color256(rest[1]), 2
	}
	if len(rest) >= 4 && rest[0] == 2 {
		return fmt.Sprintf("#%02x%02x%02x", clamp8(rest[1]), clamp8(rest[2]), clamp8(rest[3])), 4
	}
	return "", 0
}

func clamp8(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// palette16: the 8 normal + 8 bright ANSI colors, tuned to the dark UI.
var palette16 = [16]string{
	"#1e1e1e", "#e0685a", "#67c98a", "#e5c07b", "#6ea8ff", "#c678dd", "#56b6c2", "#d6d8dc",
	"#5c6370", "#f07178", "#8ce99a", "#f2d479", "#82b1ff", "#e2a0f5", "#7fdbca", "#ffffff",
}

// color256 maps an xterm 256-color index: 0-15 palette, 16-231 6x6x6 cube,
// 232-255 grayscale ramp.
func color256(n int) string {
	switch {
	case n < 0 || n > 255:
		return ""
	case n < 16:
		return palette16[n]
	case n < 232:
		n -= 16
		lv := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", lv(n/36), lv((n/6)%6), lv(n%6))
	default:
		v := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/server/ -run TestANSI -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/ansi.go internal/server/ansi_test.go
git commit -m "feat(ui): whitelist ANSI SGR to HTML converter for screen frames"
```

---

### Task 3: screen feed (per-run poller + fan-out)

**Files:**
- Create: `internal/server/screen.go`
- Modify: `internal/server/server.go:22-45` (Server gains `screens`, built in `New`)
- Test: `internal/server/screen_test.go`

**Interfaces:**
- Consumes: `spawn.Screen`, `ansiToHTML`.
- Produces: `type screenFrame struct{HTML template.HTML; Activity int64; Dead bool; Cols, Rows int; Error string}` (json tags `html activity dead cols rows error`), `frameOf(sc spawn.Screen, err error) (screenFrame, string)`, `newScreenFeed(capture func(spawn.Handle) (spawn.Screen, error)) *screenFeed`, `(*screenFeed).Subscribe(runID int64, h spawn.Handle) (<-chan screenFrame, func())`, package var `screenPollInterval`.

- [ ] **Step 1: Write the failing tests** (`internal/server/screen_test.go`)

```go
package server

import (
	"strings"
	"sync"
	"testing"
	"time"

	"erbrus/internal/spawn"
)

func fastPoll(t *testing.T) {
	t.Helper()
	old := screenPollInterval
	screenPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { screenPollInterval = old })
}

func recvFrame(t *testing.T, ch <-chan screenFrame) screenFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return screenFrame{}
	}
}

func TestScreenFeedSendsOnChangeOnly(t *testing.T) {
	fastPoll(t)
	var mu sync.Mutex
	raw, calls := "one", 0
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return spawn.Screen{Raw: raw, Cols: 80, Rows: 24}, nil
	})
	ch, cancel := feed.Subscribe(7, "s:5")
	if f := recvFrame(t, ch); !strings.Contains(string(f.HTML), "one") || f.Cols != 80 {
		t.Fatalf("first frame = %+v", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("frame without change: %+v", f)
	case <-time.After(40 * time.Millisecond):
	}
	mu.Lock()
	raw = "two"
	mu.Unlock()
	if f := recvFrame(t, ch); !strings.Contains(string(f.HTML), "two") {
		t.Fatalf("changed frame = %+v", f)
	}
	cancel()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != n {
		t.Fatalf("still polling after last unsubscribe: %d -> %d", n, calls)
	}
}

func TestScreenFeedLateSubscriberGetsCurrentFrame(t *testing.T) {
	fastPoll(t)
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		return spawn.Screen{Raw: "steady"}, nil
	})
	ch1, cancel1 := feed.Subscribe(7, "s:5")
	defer cancel1()
	recvFrame(t, ch1)
	ch2, cancel2 := feed.Subscribe(7, "s:5")
	defer cancel2()
	if f := recvFrame(t, ch2); !strings.Contains(string(f.HTML), "steady") {
		t.Fatalf("late frame = %+v", f)
	}
}

func TestScreenFeedReportsCaptureError(t *testing.T) {
	fastPoll(t)
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		return spawn.Screen{}, errTest("can't find window")
	})
	ch, cancel := feed.Subscribe(7, "s:5")
	defer cancel()
	f := recvFrame(t, ch)
	if f.Error != "screen unavailable: can't find window" {
		t.Fatalf("error frame = %+v", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("repeated error frame: %+v", f)
	case <-time.After(40 * time.Millisecond):
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run TestScreenFeed`
Expected: compile error, `newScreenFeed` undefined.

- [ ] **Step 3: Implement** (`internal/server/screen.go`)

```go
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"sync"
	"time"

	"erbrus/internal/spawn"
)

// screenFrame is one rendered snapshot of a run's terminal as sent to the
// screen page (initial render and every SSE update).
type screenFrame struct {
	HTML     template.HTML `json:"html"`
	Activity int64         `json:"activity"` // unix seconds; 0 when unknown
	Dead     bool          `json:"dead"`
	Cols     int           `json:"cols"`
	Rows     int           `json:"rows"`
	Error    string        `json:"error,omitempty"`
}

// frameOf renders a capture (or its error) and returns the frame plus a
// fingerprint that changes iff the visible state changed. The HTML is not
// part of the fingerprint: hashing the raw screen is cheaper and equivalent.
func frameOf(sc spawn.Screen, err error) (screenFrame, string) {
	if err != nil {
		msg := "screen unavailable: " + err.Error()
		return screenFrame{Error: msg}, "err:" + msg
	}
	var act int64
	if !sc.Activity.IsZero() {
		act = sc.Activity.Unix()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%t|%d|%d|%s", act, sc.Dead, sc.Cols, sc.Rows, sc.Raw)))
	return screenFrame{
		HTML: ansiToHTML(sc.Raw), Activity: act, Dead: sc.Dead, Cols: sc.Cols, Rows: sc.Rows,
	}, hex.EncodeToString(sum[:])
}

// screenPollInterval paces the capture loop of a viewed run. 500ms is
// snappy enough to follow a TUI and cheap: one tmux exec per tick, only
// while someone has the page open.
var screenPollInterval = 500 * time.Millisecond

// screenFeed polls Capture for runs that have at least one viewer and fans
// frames out to them. Frames stay off the global Hub on purpose: they are
// kilobytes each, several per second, and the Hub drops on a full buffer —
// chat events must never lose to screen traffic.
type screenFeed struct {
	capture func(spawn.Handle) (spawn.Screen, error)
	mu      sync.Mutex
	runs    map[int64]*screenPoll
}

type screenPoll struct {
	handle spawn.Handle
	subs   map[chan screenFrame]struct{}
	last   screenFrame
	lastFP string
	stop   chan struct{}
}

func newScreenFeed(capture func(spawn.Handle) (spawn.Screen, error)) *screenFeed {
	return &screenFeed{capture: capture, runs: map[int64]*screenPoll{}}
}

// Subscribe returns a channel of frames for runID (current frame first,
// then one per change) and a cancel that also stops polling when the last
// viewer leaves.
func (f *screenFeed) Subscribe(runID int64, h spawn.Handle) (<-chan screenFrame, func()) {
	ch := make(chan screenFrame, 4)
	f.mu.Lock()
	p, ok := f.runs[runID]
	if !ok {
		p = &screenPoll{handle: h, subs: map[chan screenFrame]struct{}{}, stop: make(chan struct{})}
		f.runs[runID] = p
		go f.loop(p)
	}
	p.subs[ch] = struct{}{}
	if p.lastFP != "" {
		ch <- p.last // buffer is empty: cannot block
	}
	f.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(p.subs, ch)
			if len(p.subs) == 0 {
				close(p.stop)
				if f.runs[runID] == p {
					delete(f.runs, runID)
				}
			}
		})
	}
}

func (f *screenFeed) loop(p *screenPoll) {
	f.tick(p)
	t := time.NewTicker(screenPollInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			f.tick(p)
		}
	}
}

// tick captures once and fans the frame out iff it differs from the last.
// Slow viewers drop frames (each frame is a full screen, so the next one
// heals them).
func (f *screenFeed) tick(p *screenPoll) {
	sc, err := f.capture(p.handle)
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-p.stop:
		return
	default:
	}
	frame, fp := frameOf(sc, err)
	if fp == p.lastFP {
		return
	}
	p.last, p.lastFP = frame, fp
	for ch := range p.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}
```

In `internal/server/server.go`, add `screens *screenFeed` to `Server` (after `hub`) and change `New` to:

```go
func New(st *store.Store, cfg config.Global, run wt.Runner) *Server {
	s := &Server{
		st:      st,
		cfg:     cfg,
		run:     run,
		dataDir: cfg.ResolvedDataDir(),
		hub:     NewHub(),
		pages:   web.Pages(template.FuncMap{"localtime": localTime}),
	}
	// Resolved per call: SetRuntime wires the spawner after New.
	s.screens = newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		if s.spawner == nil {
			return spawn.Screen{}, errors.New("no spawner configured")
		}
		return s.spawner.Capture(h)
	})
	return s
}
```

(add `"errors"` to imports).

Note on `frameOf` in `tick`: compute `frameOf` before taking the lock if profiling ever shows the HTML render matters; at 500ms per viewed run it does not.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/server/ -run 'TestScreenFeed|TestANSI'`
Expected: PASS, no race reports.

- [ ] **Step 5: Commit**

```bash
git add internal/server/screen.go internal/server/screen_test.go internal/server/server.go
git commit -m "feat(ui): per-run screen feed — poll Capture only for viewed runs, fan out on change"
```

---

### Task 4: screen page, per-run SSE endpoint, runs-panel link

**Files:**
- Modify: `internal/server/screen.go` (handlers appended)
- Modify: `internal/server/server.go:96-109` (two routes)
- Create: `internal/web/templates/screen.html`
- Modify: `internal/web/templates/_runs.html`
- Modify: `internal/web/static/app.js` (append a second IIFE)
- Modify: `internal/web/static/app.css` (append)
- Test: `internal/server/screen_test.go` (append)

**Interfaces:**
- Consumes: `screenFeed.Subscribe`, `frameOf`, `runningAgent`/`twoChannels`/`noRedirect` helpers from `compose_test.go`, `fakeSpawner.setScreen` from Task 1, `pingInterval` from `sse.go`.
- Produces: `GET /ui/runs/{id}/screen` (page), `GET /ui/runs/{id}/screen/events` (SSE: `frame`, `ping`).

- [ ] **Step 1: Write the failing tests** (append to `internal/server/screen_test.go`; add imports `bufio context fmt net/http erbrus/internal/store`)

```go
func TestScreenPageRendersCapture(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	fs.setScreen("s:5", spawn.Screen{Raw: "\x1b[1mhello\x1b[0m <b>", Activity: time.Unix(1788820410, 0)})
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")

	resp, err := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		fmt.Sprintf(`id="screen" data-run="%d"`, run.ID),
		`<span style="font-weight:bold">hello</span> &lt;b&gt;`,
		`data-activity="1788820410"`,
		"claude", "s:5",
		fmt.Sprintf(`/ui/channels/%d`, ch1),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The runs panel links to the page.
	presp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if pb := readAll(t, presp); !strings.Contains(pb, fmt.Sprintf(`href="/ui/runs/%d/screen"`, run.ID)) {
		t.Errorf("runs panel lacks Screen link: %s", pb)
	}
}

func TestScreenPageNoWindow(t *testing.T) {
	ts, st, root := newTestServer(t)
	testSrv.SetRuntime(&fakeSpawner{}, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run, err := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex", AgentName: "fg",
		Workdir: "/w", Status: "running", Spawner: "fg"})
	if err != nil {
		t.Fatal(err)
	}
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(body, "no tmux window for this run") {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	eresp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen/events", ts.URL, run.ID))
	eresp.Body.Close()
	if eresp.StatusCode != http.StatusNotFound {
		t.Fatalf("events status = %d", eresp.StatusCode)
	}
	if r404, _ := http.Get(ts.URL + "/ui/runs/999999/screen"); r404.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown run status = %d", r404.StatusCode)
	}
}

func TestScreenEventsStreamFrames(t *testing.T) {
	fastPoll(t)
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	fs.setScreen("s:5", spawn.Screen{Raw: "first"})
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/ui/runs/%d/screen/events", ts.URL, run.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	nextFrame := func() string {
		for sc.Scan() {
			if sc.Text() == "event: frame" && sc.Scan() {
				return strings.TrimPrefix(sc.Text(), "data: ")
			}
		}
		t.Fatalf("stream ended: %v", sc.Err())
		return ""
	}
	if d := nextFrame(); !strings.Contains(d, `"html":"first"`) {
		t.Fatalf("first frame = %s", d)
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "second", Dead: true})
	if d := nextFrame(); !strings.Contains(d, `"html":"second"`) || !strings.Contains(d, `"dead":true`) {
		t.Fatalf("second frame = %s", d)
	}
}
```

Add a `readAll` helper if `server_test.go` lacks one (check first with `grep -n 'func readAll' internal/server/*_test.go`):

```go
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run TestScreen`
Expected: 404s / missing template — the routes and page do not exist.

- [ ] **Step 3: Implement handlers** (append to `internal/server/screen.go`; add imports `encoding/json net/http erbrus/internal/store`)

```go
type screenPage struct {
	Run     store.AgentRun
	Live    bool
	Channel store.Channel
	Project store.Project
	Frame   screenFrame
	// Note replaces the live view when there is nothing to poll.
	Note string
}

func (s *Server) handleUIScreen(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	ch, _, _ := s.st.ChannelByID(run.ChannelID)
	pr, _, _ := s.st.ProjectByID(ch.ProjectID)
	page := screenPage{Run: run, Live: run.Status == "starting" || run.Status == "running", Channel: ch, Project: pr}
	switch {
	case run.TmuxTarget == "":
		page.Note = "no tmux window for this run"
	case s.spawner == nil:
		page.Note = "no spawner configured"
	default:
		sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
		page.Frame, _ = frameOf(sc, err)
	}
	s.render(w, "screen", page)
}

// handleUIScreenEvents streams frames for one run to one viewer. Separate
// from /events so screen traffic never competes with chat events.
func (s *Server) handleUIScreenEvents(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || run.TmuxTarget == "" {
		httpError(w, http.StatusNotFound, "run has no tmux window")
		return
	}
	if s.spawner == nil {
		httpError(w, http.StatusServiceUnavailable, "no spawner configured")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	frames, cancel := s.screens.Subscribe(run.ID, spawn.Handle(run.TmuxTarget))
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			fl.Flush()
		case f := <-frames:
			b, err := json.Marshal(f)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: frame\ndata: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
```

Routes in `internal/server/server.go`, next to `/ui/channels/{id}/runs-panel`:

```go
	r.Get("/ui/runs/{id}/screen", s.handleUIScreen)
	r.Get("/ui/runs/{id}/screen/events", s.handleUIScreenEvents)
```

- [ ] **Step 4: Templates and assets**

`internal/web/templates/screen.html`:

```html
{{define "title"}}{{.Run.AgentName}} screen · erbrus{{end}}
{{define "content"}}
<header class="topbar">
  <a class="brand" href="/ui/projects">erbrus</a>
  <span class="crumb">/ {{.Project.Name}} / <a href="/ui/channels/{{.Channel.ID}}"># {{.Channel.Name}}</a> / {{.Run.AgentName}}</span>
  <span class="spacer"></span>
  <a class="btn" href="/ui/channels/{{.Channel.ID}}">Back to channel</a>
</header>
<main class="screenpage">
  <div class="screenhead" id="screenhead">
    <strong>{{.Run.AgentName}}</strong>
    <span class="muted">{{.Run.Provider}} · {{.Run.Status}}{{if .Run.TmuxTarget}} · <span class="mono">{{.Run.TmuxTarget}}</span>{{end}}</span>
    <span class="spacer"></span>
    <span id="screenact" class="muted small" data-activity="{{.Frame.Activity}}"></span>
    <span id="screenerr" class="small warn">{{if .Note}}{{.Note}}{{else if .Frame.Error}}{{.Frame.Error}}{{else if .Frame.Dead}}process exited — final screen{{end}}</span>
  </div>
  <pre class="screen" id="screen" data-run="{{.Run.ID}}"{{if .Note}} data-static="1"{{end}}>{{.Frame.HTML}}</pre>
</main>
{{end}}
```

`internal/web/templates/_runs.html`: add the link in both branches, right before each `<form`:

```html
  {{if .TmuxTarget}}<a class="btn small" href="/ui/runs/{{.ID}}/screen">Screen</a>{{end}}
```

Append to `internal/web/static/app.js` (after the channel IIFE's closing `})();`):

```js
// Screen view: one EventSource per open screen page, frames swap the <pre>.
// The activity age is computed client-side from the last frame's unix
// timestamp so the server only talks when the screen actually changes.
(function () {
  var screen = document.getElementById('screen');
  if (!screen || screen.dataset.static) return; // not a live screen page
  var runID = screen.dataset.run;
  var head = document.getElementById('screenhead');
  var act = document.getElementById('screenact');
  var errEl = document.getElementById('screenerr');
  var idleAfter = 60; // seconds without tmux activity before the header turns amber
  var activity = parseInt(act.dataset.activity, 10) || 0;

  function tickAge() {
    if (!activity) { act.textContent = ''; head.classList.remove('idle'); return; }
    var age = Math.max(0, Math.floor(Date.now() / 1000 - activity));
    act.textContent = 'last activity ' + age + 's ago';
    head.classList.toggle('idle', age > idleAfter);
  }
  setInterval(tickAge, 1000);
  tickAge();

  var es = null;
  var lastSeen = Date.now();
  function alive() { lastSeen = Date.now(); }
  function connect() {
    if (es) es.close();
    es = new EventSource('/ui/runs/' + runID + '/screen/events');
    es.onopen = alive;
    es.addEventListener('ping', alive);
    es.addEventListener('frame', function (e) {
      alive();
      try {
        var f = JSON.parse(e.data);
        if (f.error) { errEl.textContent = f.error; return; }
        errEl.textContent = f.dead ? 'process exited — final screen' : '';
        screen.innerHTML = f.html;
        activity = f.activity || 0;
        tickAge();
      } catch (err) { /* ignore malformed */ }
    });
  }
  connect();
  setInterval(function () {
    if (Date.now() - lastSeen > 45000) { alive(); connect(); }
  }, 15000);
})();
```

Append to `internal/web/static/app.css`:

```css
/* screen view */
.screenpage { padding: 12px 18px; }
.screenhead { display: flex; align-items: center; gap: 10px; padding: 6px 10px; margin-bottom: 8px; background: #1d1f23; border: 1px solid #2c2f35; border-radius: 6px; }
.screenhead.idle { border-color: #b8860b; background: #2a2416; }
.screenhead .warn { color: #e5c07b; }
/* colors here are mirrored by screenFg/screenBg in ansi.go (reverse video) */
.screen { margin: 0; padding: 10px; font: 12px/1.35 ui-monospace, monospace; color: #d6d8dc; background: #0c0d0f; border: 1px solid #2c2f35; border-radius: 6px; overflow: auto; white-space: pre; min-height: 300px; }
```

- [ ] **Step 5: Run tests**

Run: `go test -race ./...`
Expected: PASS. (`TestUIChannel`-style tests that count runs-panel substrings still pass; the Screen link only adds content.)

- [ ] **Step 6: Commit**

```bash
git add internal/server/screen.go internal/server/screen_test.go internal/server/server.go internal/server/server_test.go internal/web/templates/screen.html internal/web/templates/_runs.html internal/web/static/app.js internal/web/static/app.css
git commit -m "feat(ui): live read-only screen page per run (/ui/runs/{id}/screen) with per-run SSE"
```

---

### Task 5: build, live check, hand-over

**Files:** none (verification only).

- [ ] **Step 1: Format and full suite**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: no files listed, no vet findings, PASS.

- [ ] **Step 2: Build**

Run: `./build.sh`
Expected: `bin/erbrus` and `bin/erbrus.exe` listed.

- [ ] **Step 3: Live check against a real window (read-only tmux use)**

With the user's `erbrus serve` NOT restarted (shared process rule), verify the converter against a live capture instead:

```bash
tmux capture-pane -p -e -t '=0:7' > /tmp/claude-1000/-mnt-t-others-erbrus/53b7082d-2cb8-4fe3-8570-f92116321892/scratchpad/live.ansi
go run ./internal/tools/ansicheck 2>/dev/null || true
```

If no `ansicheck` tool exists (it does not), write a throwaway `_test.go` under the scratchpad that calls `ansiToHTML` on the file and fails on any remaining `\x1b`; delete it afterwards. Report the result in the hand-over.

- [ ] **Step 4: Hand-over message**

Absolute binary path `/mnt/t/others/erbrus/bin/erbrus`, restart needed (`erbrus serve`), where the Screen link is, what the amber header means (tmux reported no output for >60 s, which for a spinner-driven TUI means frozen, not merely thinking), and that classification of "awaiting input" is round 2.
