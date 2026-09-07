# Screen State Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Classify each running agent's tmux screen as working / waiting / question (+ stalled), show it on the run card and screen page, and post one system message per meaningful transition.

**Architecture:** New `internal/screen` package (pure: ANSI strip/HTML, tail, regex rules, classify, step-duration parse). `internal/server` compiles per-provider rules once, runs a watcher in the existing reconcile tick over running tmux runs, keeps in-memory state per run, and notifies via the existing `system()` + `hub.Publish("run")` primitives. The screen feed classifies too so the page header agrees with the rail.

**Tech Stack:** Go 1.26, stdlib `regexp` (RE2). Zero new dependencies.

**Spec:** /mnt/t/others/erbrus/docs/superpowers/specs/2026-09-08-screen-state-design.md

## Global Constraints

- Direct on `main`, commit per task, never push. Rebuild `bin/erbrus` at the end.
- Rules compile once at `server.New`; never per tick.
- Never act on `screen.Unknown`.
- Frames and states never go through `Hub` except the small `run` event already used by the rail.
- `stallAfter` = 120 s package var; reconcile/watch interval = 10 s in `serve.go`.

---

### Task 1: `internal/screen` package (move ANSI, add classifier)

**Files:**
- Move: `internal/server/ansi.go` → `internal/screen/ansi.go` (package `screen`; `ToHTML(raw string) string`, `Strip(raw string) string`, shared `scan`)
- Move: `internal/server/ansi_test.go` → `internal/screen/ansi_test.go` (rename calls to `ToHTML`)
- Create: `internal/screen/classify.go`, `internal/screen/classify_test.go`
- Modify: `internal/server/screen.go` (`frameOf` uses `template.HTML(screen.ToHTML(sc.Raw))`)

**Interfaces produced:**
```go
package screen
type State string // "", "working", "waiting", "question"
const ( Unknown State = ""; Working State = "working"; Waiting State = "waiting"; Question State = "question" )
type Rules struct{ Working, Waiting, Question []*regexp.Regexp }
func DefaultRules(provider string) Rules
func Compile(working, waiting, question []string) (Rules, error)
func Strip(raw string) string
func ToHTML(raw string) string
func Tail(text string, n int) []string          // last n non-empty trimmed lines
func Classify(r Rules, lines []string) State
func StepDuration(lines []string) time.Duration // 0 when no timer
```

- [ ] **Step 1: Move the converter** — `git mv` both files, change `package server` → `package screen`, rename `ansiToHTML` → `ToHTML` returning `string` (drop the `html/template` import), refactor the byte walk into

```go
// scan walks raw once: text receives printable bytes in order, sgr receives
// the parameter string of each SGR sequence. Every other escape is dropped.
func scan(raw string, text func(byte), sgr func(params string))
```

with `ToHTML` and `Strip` both built on it:

```go
// Strip returns the screen with every escape sequence removed.
func Strip(raw string) string {
	var b strings.Builder
	scan(raw, func(c byte) { b.WriteByte(c) }, func(string) {})
	return b.String()
}
```

Update `internal/server/screen.go`: import `erbrus/internal/screen`, `HTML: template.HTML(screen.ToHTML(sc.Raw))`. Run `go test ./internal/screen/ ./internal/server/` — must pass before adding anything.

- [ ] **Step 2: Failing classifier tests** (`internal/screen/classify_test.go`)

```go
package screen

import (
	"strings"
	"testing"
	"time"
)

// Captured 2026-09-08 from live Claude Code windows (ANSI already stripped).
const workingScreen = `  ⎿  Running…
✻ Cogitating… (27s · ↓ 1.5k tokens · thought for 1s)
  ⎿  Tip: Use git worktrees to run multiple Claude sessions in parallel.
──────────────────────────────────────────────────────────────────────
❯ 
──────────────────────────────────────────────────────────────────────
  homeend@homeend-p14s  /mnt/t/others/erbrus (main)  Fable 5.1  19% of 1M
  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 2 agents
`

const waitingScreen = `※ recap: Adding parent checkout rows is done. Do you want to proceed with
  the upload when you are ready? Press Enter when done. (disable recaps in
  /config)
──────────────────────────────────────────────────────────────────────
❯ 
──────────────────────────────────────────────────────────────────────
  homeend@homeend-p14s  ~/git-focus (master)  Fable 5.1  14% of 1M
  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 2 agents
`

const questionScreen = `⏺ I need to delete the old migration file first.

 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and don't ask again this session
   3. No, and tell Claude what to do differently
 Esc to cancel
`

func TestTail(t *testing.T) {
	got := Tail("a\n\n  b  \nc\n\n", 2)
	if strings.Join(got, "|") != "b|c" {
		t.Errorf("got %v", got)
	}
}

func TestClassifyClaudeCode(t *testing.T) {
	r := DefaultRules("claude-code")
	cases := map[string]State{
		workingScreen:  Working,
		waitingScreen:  Waiting, // scrollback phrases must not win over the prompt box
		questionScreen: Question,
		"":             Unknown,
		"just some text\nno prompt": Unknown,
	}
	for in, want := range cases {
		if got := Classify(r, Tail(in, 15)); got != want {
			t.Errorf("%q: got %q want %q", in[:min(len(in), 30)], got, want)
		}
	}
}

func TestClassifyStripsBeforeMatching(t *testing.T) {
	raw := "\x1b[38;5;246m❯ \x1b[39m\n"
	if got := Classify(DefaultRules("claude-code"), Tail(Strip(raw), 15)); got != Waiting {
		t.Errorf("got %q", got)
	}
}

func TestClassifyGenericAndCodex(t *testing.T) {
	if got := Classify(DefaultRules("mystery"), Tail("run tests? (y/n)", 5)); got != Question {
		t.Errorf("generic question: %q", got)
	}
	if got := Classify(DefaultRules("mystery"), Tail("$ ", 5)); got != Waiting {
		t.Errorf("generic waiting: %q", got)
	}
	if got := Classify(DefaultRules("codex"), Tail("• Working (12s • esc to interrupt)\n›", 5)); got != Working {
		t.Errorf("codex working: %q", got)
	}
}

func TestCompileOverridesAndErrors(t *testing.T) {
	r, err := Compile([]string{`Thinking \(`}, []string{`^>\s*$`}, []string{`CONFIRM`})
	if err != nil {
		t.Fatal(err)
	}
	if got := Classify(r, Tail("please CONFIRM", 5)); got != Question {
		t.Errorf("override: %q", got)
	}
	if _, err := Compile([]string{`(`}, nil, nil); err == nil {
		t.Error("bad pattern accepted")
	}
}

func TestStepDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"✻ Cogitating… (27s · ↓ 1.5k tokens)": 27 * time.Second,
		"✻ Precipitating… (7m 5s · ↓ 5.2k)":   7*time.Minute + 5*time.Second,
		"✶ Working… (1h 2m 3s · x)":            time.Hour + 2*time.Minute + 3*time.Second,
		"no timer here":                        0,
	}
	for in, want := range cases {
		if got := StepDuration([]string{in}); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}
```

- [ ] **Step 3: Implement** (`internal/screen/classify.go`)

```go
package screen

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// State is what the bottom of an agent's screen says it is doing.
type State string

const (
	Unknown  State = ""         // blank, mid-redraw, or nothing matched
	Working  State = "working"  // spinner / running step visible
	Waiting  State = "waiting"  // empty prompt: turn finished
	Question State = "question" // a dialog wants a decision
)

// Rules are the per-provider patterns. Checks are ordered Working →
// Waiting → Question so that a phrase quoted in scrollback can never
// outrank the prompt box that is still on screen.
type Rules struct {
	Working, Waiting, Question []*regexp.Regexp
}

var defaults = map[string][3][]string{
	"claude-code": {
		{`\S+… \(\d+`, `⎿\s+Running…`},
		{`^❯\s*$`},
		{`^❯ \d+\.`, `Esc to cancel`, `Esc to go back`, `\(y/n\)`, `\[Y/n\]`, `Do you want to proceed`},
	},
	"codex": {
		{`Working \(\d+`, `esc to interrupt`},
		{`^[›>]\s*$`},
		{`\(y/n\)`, `\[Y/n\]`, `Press Enter`, `^\s*[›>] \d+\.`},
	},
	"": { // generic
		nil,
		{`^[❯›>$]\s*$`},
		{`\(y/n\)`, `\[Y/n\]`, `Press Enter`, `Do you want to`},
	},
}

// DefaultRules returns the built-in rules for a provider name, or the
// generic set for names erbrus does not know.
func DefaultRules(provider string) Rules {
	d, ok := defaults[provider]
	if !ok {
		d = defaults[""]
	}
	r, _ := Compile(d[0], d[1], d[2]) // built-ins are tested to compile
	return r
}

// Compile builds Rules from RE2 pattern lists (config overrides).
func Compile(working, waiting, question []string) (Rules, error) {
	var r Rules
	var err error
	if r.Working, err = compileAll(working); err != nil {
		return Rules{}, err
	}
	if r.Waiting, err = compileAll(waiting); err != nil {
		return Rules{}, err
	}
	if r.Question, err = compileAll(question); err != nil {
		return Rules{}, err
	}
	return r, nil
}

func compileAll(ps []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(ps))
	for _, p := range ps {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("screen pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Tail returns the last n non-empty, whitespace-trimmed lines of text.
func Tail(text string, n int) []string {
	all := strings.Split(text, "\n")
	out := make([]string, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		if l := strings.TrimSpace(all[i]); l != "" {
			out = append(out, l)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Classify applies r to the tail lines in structural order.
func Classify(r Rules, lines []string) State {
	switch {
	case anyMatch(r.Working, lines):
		return Working
	case anyMatch(r.Waiting, lines):
		return Waiting
	case anyMatch(r.Question, lines):
		return Question
	}
	return Unknown
}

func anyMatch(res []*regexp.Regexp, lines []string) bool {
	for _, re := range res {
		for _, l := range lines {
			if re.MatchString(l) {
				return true
			}
		}
	}
	return false
}

// stepRe matches Claude Code's spinner timer: "… (27s ·", "… (7m 5s ·",
// "… (1h 2m 3s ·".
var stepRe = regexp.MustCompile(`… \((?:(\d+)h )?(?:(\d+)m )?(\d+)s`)

// StepDuration reports how long the current step has been running
// according to the spinner line, 0 when there is none.
func StepDuration(lines []string) time.Duration {
	for _, l := range lines {
		m := stepRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		var d time.Duration
		for i, unit := range []time.Duration{time.Hour, time.Minute, time.Second} {
			if m[i+1] != "" {
				var n int
				fmt.Sscanf(m[i+1], "%d", &n)
				d += time.Duration(n) * unit
			}
		}
		return d
	}
	return 0
}
```

Note: `Tail` uses `strings.TrimSpace`, so the `❯ ` line becomes `❯` and `^❯\s*$` matches.

- [ ] **Step 4: Run** `go test -race ./internal/screen/ ./internal/server/` — PASS.
- [ ] **Step 5: Commit** `feat(screen): classifier package — strip/tail/classify/step duration; ANSI converter moved`

---

### Task 2: config overrides + rules compiled in `server.New`

**Files:**
- Modify: `internal/config/config.go:48-51` (Provider fields)
- Modify: `internal/server/server.go` (`rules map[string]screen.Rules`, `rulesFor`)
- Test: `internal/server/watch_test.go` (created here, extended in Task 3)

- [ ] **Step 1: Failing test**

```go
package server

import (
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/screen"
)

func TestRulesForHonorsConfigOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers = map[string]config.Provider{
		"kimi":   {Command: "kimi", ScreenQuestion: []string{"CONFIRM"}},
		"broken": {Command: "x", ScreenWorking: []string{"("}}, // falls back to generic
	}
	s := New(nil, cfg, nil)
	if got := screen.Classify(s.rulesFor("kimi"), []string{"please CONFIRM"}); got != screen.Question {
		t.Errorf("kimi override: %q", got)
	}
	if got := screen.Classify(s.rulesFor("kimi"), []string{"❯"}); got != screen.Unknown {
		t.Errorf("override must replace the whole set: %q", got)
	}
	if got := screen.Classify(s.rulesFor("broken"), []string{"(y/n)"}); got != screen.Question {
		t.Errorf("broken falls back to generic: %q", got)
	}
	if got := screen.Classify(s.rulesFor("claude-code"), []string{"❯"}); got != screen.Waiting {
		t.Errorf("unconfigured provider uses built-ins: %q", got)
	}
}
```

- [ ] **Step 2: Implement**

`config.Provider`:
```go
type Provider struct {
	Command      string `yaml:"command"`
	DefaultModel string `yaml:"default_model"`
	// Screen* are RE2 patterns classifying the agent's terminal (see
	// internal/screen). Any non-empty list replaces ALL built-in rules
	// for this provider; all empty = built-ins for this provider name.
	ScreenWorking  []string `yaml:"screen_working"`
	ScreenWaiting  []string `yaml:"screen_waiting"`
	ScreenQuestion []string `yaml:"screen_question"`
}
```

`server.go`: field `rules map[string]screen.Rules`; in `New` after the struct literal:
```go
	s.rules = map[string]screen.Rules{}
	for name, p := range cfg.Providers {
		if len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) == 0 {
			continue
		}
		r, err := screen.Compile(p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erbrus: provider %s: %v (using built-in screen rules)\n", name, err)
			continue
		}
		s.rules[name] = r
	}
```
```go
// rulesFor: config override if present, else built-ins for the name.
func (s *Server) rulesFor(provider string) screen.Rules {
	if r, ok := s.rules[provider]; ok {
		return r
	}
	return screen.DefaultRules(provider)
}
```
(`New(nil, cfg, nil)` must not touch `st`/`run`; it does not today.)

- [ ] **Step 3: Run** `go test ./internal/server/ -run TestRulesFor` — PASS. **Commit** `feat(config): per-provider screen_* pattern overrides`.

---

### Task 3: watcher, run state, notifications, rail badge

**Files:**
- Create: `internal/server/watch.go`
- Modify: `internal/server/server.go` (fields `stateMu sync.Mutex; states map[int64]runState`, init in `New`)
- Modify: `internal/server/runs.go:StartReconcileLoop` (call `WatchScreens` after `Reconcile`)
- Modify: `internal/cli/serve.go:66` (`10*time.Second`)
- Modify: `internal/server/ui_channel.go` (`runView` gains `StateLabel`, `StateClass`; `toView` fills them)
- Modify: `internal/web/templates/_runs.html`, `internal/web/static/app.css`
- Test: `internal/server/watch_test.go`

**Interfaces produced:**
```go
type runState struct { State screen.State; Since time.Time; Stalled bool; StepFor time.Duration }
var stallAfter = 120 * time.Second
func (s *Server) WatchScreens() error
func (s *Server) observe(run store.AgentRun, sc spawn.Screen, now time.Time) // testable core
func (s *Server) stateOf(runID int64) (runState, bool)
func shortDur(d time.Duration) string // "5s", "3m", "1h12m"
```

- [ ] **Step 1: Failing tests** (append to `watch_test.go`; imports `fmt net/http strings time erbrus/internal/spawn erbrus/internal/store`)

```go
// systemBodies returns the bodies of system messages in chID, oldest first.
func systemBodies(t *testing.T, st *store.Store, chID int64) []string {
	t.Helper()
	msgs, err := st.MessagesLatest(chID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		if m.Kind == "system" {
			out = append(out, m.Body)
		}
	}
	return out
}

func countRunEvents(ev <-chan sseEvent) int {
	n := 0
	for {
		select {
		case e := <-ev:
			if e.Event == "run" {
				n++
			}
		default:
			return n
		}
	}
}

func TestWatchScreensTurnEnd(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{alive: map[spawn.Handle]bool{"s:5": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run, _ := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "claude-code", AgentName: "claude",
		Workdir: "/w", Status: "running", Spawner: "tmux", TmuxTarget: "s:5"})
	ev, cancel := testSrv.hub.Subscribe()
	defer cancel()
	before := len(systemBodies(t, st, ch1))

	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n❯\n", Activity: time.Now()})
	if err := testSrv.WatchScreens(); err != nil {
		t.Fatal(err)
	}
	if rs, ok := testSrv.stateOf(run.ID); !ok || rs.State != "working" || rs.StepFor != 27*time.Second {
		t.Fatalf("after working screen: %+v", rs)
	}
	if n := countRunEvents(ev); n != 1 {
		t.Fatalf("run events after first classify = %d", n)
	}
	if got := systemBodies(t, st, ch1); len(got) != before {
		t.Fatalf("entering working must not post: %v", got)
	}

	fs.setScreen("s:5", spawn.Screen{Raw: "done.\n❯\n", Activity: time.Now()})
	testSrv.WatchScreens()
	got := systemBodies(t, st, ch1)
	if len(got) != before+1 || !strings.Contains(got[len(got)-1], "finished its turn") {
		t.Fatalf("turn end message: %v", got)
	}
	if n := countRunEvents(ev); n != 1 {
		t.Fatalf("run events on turn end = %d", n)
	}

	testSrv.WatchScreens() // same screen: nothing
	fs.setScreen("s:5", spawn.Screen{Raw: "", Activity: time.Now()})
	testSrv.WatchScreens() // unknown: nothing
	if got := systemBodies(t, st, ch1); len(got) != before+1 {
		t.Fatalf("repeat/unknown posted: %v", got)
	}
	if n := countRunEvents(ev); n != 0 {
		t.Fatalf("run events on repeat/unknown = %d", n)
	}
	if rs, _ := testSrv.stateOf(run.ID); rs.State != "waiting" {
		t.Fatalf("unknown must keep state: %+v", rs)
	}

	// Rail badge.
	resp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if body := readAll(t, resp); !strings.Contains(body, `class="state st-waiting"`) || !strings.Contains(body, "waiting") {
		t.Fatalf("badge missing: %s", body)
	}
}

func TestWatchScreensQuestionAndStall(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run, _ := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "claude-code", AgentName: "claude",
		Workdir: "/w", Status: "running", Spawner: "tmux", TmuxTarget: "s:5"})
	before := len(systemBodies(t, st, ch1))

	fs.setScreen("s:5", spawn.Screen{Raw: "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n", Activity: time.Now()})
	testSrv.WatchScreens()
	got := systemBodies(t, st, ch1)
	if len(got) != before+1 || !strings.Contains(got[len(got)-1], "needs your input") {
		t.Fatalf("question message: %v", got)
	}

	// Working but silent for longer than stallAfter → stalled once.
	old := time.Now().Add(-stallAfter - time.Minute)
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (9m 0s · x)\n❯\n", Activity: old})
	testSrv.WatchScreens()
	testSrv.WatchScreens()
	got = systemBodies(t, st, ch1)
	if len(got) != before+2 || !strings.Contains(got[len(got)-1], "stalled") {
		t.Fatalf("stall message: %v", got)
	}
	if rs, _ := testSrv.stateOf(run.ID); !rs.Stalled {
		t.Fatalf("stalled flag: %+v", rs)
	}
	// Output resumes → flag clears, no message.
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (9m 5s · x)\n❯\n", Activity: time.Now()})
	testSrv.WatchScreens()
	if rs, _ := testSrv.stateOf(run.ID); rs.Stalled {
		t.Fatalf("stalled not cleared: %+v", rs)
	}
	if got := systemBodies(t, st, ch1); len(got) != before+2 {
		t.Fatalf("clearing stall posted: %v", got)
	}
	// Silence while waiting is not a stall.
	fs.setScreen("s:5", spawn.Screen{Raw: "❯\n", Activity: old})
	testSrv.WatchScreens()
	if rs, _ := testSrv.stateOf(run.ID); rs.Stalled {
		t.Fatalf("waiting must not stall: %+v", rs)
	}
}
```

- [ ] **Step 2: Implement** (`internal/server/watch.go`)

```go
package server

import (
	"fmt"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// runState is what the watcher last concluded about a running agent.
type runState struct {
	State   screen.State
	Since   time.Time
	Stalled bool
	StepFor time.Duration // current step per the spinner; 0 when unknown
}

// stallAfter: no terminal output for this long while (apparently) working
// is reported as a stall. Waiting and question states are silent by
// nature and never stall.
var stallAfter = 120 * time.Second

// WatchScreens classifies every running tmux run once. Called from the
// reconcile loop right after Reconcile, so windows already gone are marked
// failed before we try to capture them.
func (s *Server) WatchScreens() error {
	if s.spawner == nil {
		return nil
	}
	runs, err := s.st.RunningRuns()
	if err != nil {
		return err
	}
	live := map[int64]bool{}
	now := time.Now()
	for _, r := range runs {
		if r.Spawner != "tmux" || r.TmuxTarget == "" {
			continue
		}
		live[r.ID] = true
		sc, err := s.spawner.Capture(spawn.Handle(r.TmuxTarget))
		if err != nil {
			continue // window flapping; Reconcile handles the dead case
		}
		s.observe(r, sc, now)
	}
	s.stateMu.Lock()
	for id := range s.states {
		if !live[id] {
			delete(s.states, id)
		}
	}
	s.stateMu.Unlock()
	return nil
}

// observe folds one capture into the run's state, posting system messages
// on the transitions worth a human's attention and a run event whenever
// the rail's badge would change. Unknown screens change nothing.
func (s *Server) observe(run store.AgentRun, sc spawn.Screen, now time.Time) {
	lines := screen.Tail(screen.Strip(sc.Raw), 15)
	st := screen.Classify(s.rulesFor(run.Provider), lines)

	s.stateMu.Lock()
	prev := s.states[run.ID]
	next := prev
	changed := false
	var notes []string
	if st != screen.Unknown && st != prev.State {
		next.State, next.Since = st, now
		changed = true
		switch {
		case st == screen.Question:
			notes = append(notes, fmt.Sprintf("%s is asking a question — needs your input", run.AgentName))
		case st == screen.Waiting && prev.State == screen.Working:
			notes = append(notes, fmt.Sprintf("%s finished its turn — waiting for input", run.AgentName))
		}
	}
	next.StepFor = screen.StepDuration(lines)
	stalled := !sc.Activity.IsZero() && now.Sub(sc.Activity) > stallAfter &&
		(next.State == screen.Working || next.State == screen.Unknown)
	if stalled != prev.Stalled {
		changed = true
		if stalled {
			notes = append(notes, fmt.Sprintf("%s has printed nothing for %s — stalled?", run.AgentName, shortDur(now.Sub(sc.Activity))))
		}
	}
	next.Stalled = stalled
	s.states[run.ID] = next
	s.stateMu.Unlock()

	for _, n := range notes {
		s.system(run.ChannelID, n)
	}
	if changed {
		s.hub.Publish("run", toRunJSON(run))
	}
}

func (s *Server) stateOf(runID int64) (runState, bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	rs, ok := s.states[runID]
	return rs, ok
}

// shortDur: "5s", "3m", "1h12m" — badge-sized.
func shortDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// badge renders the rail label + CSS class for a run state.
func (rs runState) badge(now time.Time) (label, class string) {
	switch rs.State {
	case screen.Working:
		label = "working"
		if rs.StepFor > 0 {
			label += " " + shortDur(rs.StepFor)
		}
	case screen.Waiting:
		label = "waiting " + shortDur(now.Sub(rs.Since))
	case screen.Question:
		label = "needs input"
	default:
		if !rs.Stalled {
			return "", ""
		}
	}
	class = "state st-" + string(rs.State)
	if rs.Stalled {
		label = "stalled · " + label
		class += " stalled"
	}
	return label, class
}
```

`server.go`: fields `stateMu sync.Mutex; states map[int64]runState`; `s.states = map[int64]runState{}` in `New`.

`runs.go` `StartReconcileLoop` tick body: `_ = s.Reconcile(); _ = s.WatchScreens()`; comment: "Reconcile first so WatchScreens never captures a window Reconcile just declared dead."

`serve.go`: `srv.StartReconcileLoop(10*time.Second, ...)` with the comment "10s: also paces screen-state detection".

`ui_channel.go` `runView`: add `StateLabel, StateClass string`; in `toView`, when `v.Running`: `if rs, ok := s.stateOf(r.ID); ok { v.StateLabel, v.StateClass = rs.badge(time.Now()) }`.

`_runs.html`, running branch, after `<strong>{{.AgentName}}</strong>`:
```html
{{if .StateLabel}} <span class="{{.StateClass}}">{{.StateLabel}}</span>{{end}}
```

`app.css`:
```css
.state { display: inline-block; font-size: 11px; border-radius: 8px; padding: 0 7px; margin-left: 6px; border: 1px solid #3a3d44; color: #a7acb5; }
.state.st-working { border-color: #3f7d58; color: #67c98a; }
.state.st-waiting { border-color: #4a5a7a; color: #8fb4ff; }
.state.st-question, .state.stalled { border-color: #b8860b; color: #e5c07b; background: #2a2416; }
```

- [ ] **Step 3: Run** `go test -race ./...` — PASS (lifecycle tests call `Reconcile` directly; the loop change is transparent).
- [ ] **Step 4: Commit** `feat(watch): screen-state watcher — working/waiting/question/stalled badges + transition messages`

---

### Task 4: screen page shows state

**Files:**
- Modify: `internal/server/screen.go` (`screenFrame.State`, `Step`; `frameOf(sc, err, rules)`; `Subscribe(runID, h, rules)`; handlers pass `s.rulesFor(run.Provider)`)
- Modify: `internal/server/screen_test.go` (pass `screen.Rules{}` / `screen.DefaultRules("claude-code")`; assert `"state":"working"` in a frame)
- Modify: `internal/web/templates/screen.html`, `internal/web/static/app.js`, `internal/web/static/app.css`

- [ ] **Step 1: Failing test tweak** — in `TestScreenEventsStreamFrames` set `Provider: "claude-code"` by creating the run directly (as in Task 3's tests) with Raw `"✻ Cogitating… (27s · x)\n❯\n"` and assert the first frame contains `"state":"working"` and `"step":27`.
- [ ] **Step 2: Implement** — `screenFrame` gains `State string \`json:"state"\``, `Step int \`json:"step"\`` (seconds); `frameOf` computes `lines := screen.Tail(screen.Strip(sc.Raw), 15)`, `State: string(screen.Classify(rules, lines))`, `Step: int(screen.StepDuration(lines).Seconds())`; the fingerprint is unchanged (state derives from Raw). `screenPoll` stores `rules`; `Subscribe(runID, h, rules)`.

`screen.html` header: add `<span id="screenstate" class="state{{if .Frame.State}} st-{{.Frame.State}}{{end}}" data-state="{{.Frame.State}}" data-step="{{.Frame.Step}}">{{.Frame.State}}</span>` before the activity span.

`app.js` screen IIFE: keep `state`/`step` from each frame; `tickAge` sets `screenstate` text to `state + (step ? ' · step ' + fmt(step) : '')`, class `state st-<state>`, and applies `idle` only when `age > 120 && (state === 'working' || state === '')` (comment: mirrors `stallAfter` in watch.go).

- [ ] **Step 3: Run** `go test -race ./...` — PASS. **Commit** `feat(ui): screen page header shows live state and step duration`.

---

### Task 5: build, hand-over

- [ ] `gofmt -l . && go vet ./... && go test -race ./...` clean.
- [ ] `./build.sh`.
- [ ] Hand-over: binary path, restart needed, what each badge means, the 120 s stall threshold and 10 s detection cadence, config override syntax, the untested-live-dialog and unverified-codex caveats, spec/plan paths.
