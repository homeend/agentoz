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
		workingScreen:               Working,
		waitingScreen:               Waiting, // scrollback phrases must not win over the prompt box
		questionScreen:              Question,
		"":                          Unknown,
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
		"✶ Working… (1h 2m 3s · x)":           time.Hour + 2*time.Minute + 3*time.Second,
		"no timer here":                       0,
	}
	for in, want := range cases {
		if got := StepDuration([]string{in}); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}
