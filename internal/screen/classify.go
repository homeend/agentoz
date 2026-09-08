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
// outrank the prompt box that is still on screen. Patterns run in
// multi-line mode over the joined tail lines, so ^ and $ bound a line and
// "\n" lets a rule span two adjacent lines (e.g. rule-line + prompt).
type Rules struct {
	Working, Waiting, Question []*regexp.Regexp
}

// defaults: [working, waiting, question] pattern lists per provider name.
// "" is the generic fallback. Claude Code entries come from live captures
// (2026-09-08); codex entries are from its TUI's known strings, unverified.
var defaults = map[string][3][]string{
	"claude-code": {
		{`\S+… \(\d+`, `⎿\s+Running…`},
		// The input box is a ❯ line directly under a horizontal rule —
		// whether or not text is typed in it (an unsubmitted message
		// still means the agent is idle). A user message echoed in the
		// transcript also starts with ❯ but has no rule above it.
		{`^─{8,}\n❯`},
		{`^❯ \d+\.`, `Esc to cancel`, `Esc to go back`, `\(y/n\)`, `\[Y/n\]`, `Do you want to proceed`},
	},
	"codex": {
		{`Working \(\d+`, `esc to interrupt`},
		{`^[›>]\s*$`},
		{`\(y/n\)`, `\[Y/n\]`, `Press Enter`, `^\s*[›>] \d+\.`},
	},
	"": {
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
	r, err := Compile(d[0], d[1], d[2])
	if err != nil {
		panic(err) // built-ins are constants; a bad one is a programming error
	}
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
		re, err := regexp.Compile("(?m)" + p)
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
	text := strings.Join(lines, "\n")
	switch {
	case anyMatch(r.Working, text):
		return Working
	case anyMatch(r.Waiting, text):
		return Waiting
	case anyMatch(r.Question, text):
		return Question
	}
	return Unknown
}

func anyMatch(res []*regexp.Regexp, text string) bool {
	for _, re := range res {
		if re.MatchString(text) {
			return true
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

// HasDefaults reports whether erbrus ships rules for this provider name.
func HasDefaults(provider string) bool { _, ok := defaults[provider]; return ok }

// Patterns renders compiled rules back to one pattern per line, without
// the (?m) prefix Compile adds — the form on the settings page.
func Patterns(res []*regexp.Regexp) string {
	out := make([]string, 0, len(res))
	for _, re := range res {
		out = append(out, strings.TrimPrefix(re.String(), "(?m)"))
	}
	return strings.Join(out, "\n")
}

// MatchKind reports which list of r a single line matches, checked in
// the same order as Classify ("" when none) — for the rule tester.
func MatchKind(r Rules, line string) string {
	switch {
	case anyMatch(r.Working, line):
		return string(Working)
	case anyMatch(r.Waiting, line):
		return string(Waiting)
	case anyMatch(r.Question, line):
		return string(Question)
	}
	return ""
}

// Option is one numbered choice of a dialog ("› 1. Try new model").
type Option struct {
	Key   string `json:"key"` // the digit to press
	Label string `json:"label"`
}

var optionRe = regexp.MustCompile(`^[❯›>]?\s*(\d)[.)]\s+(.+)$`)

// Options extracts the numbered choices a dialog offers, in screen order,
// so the UI can show real buttons instead of a fixed 1/2/3.
func Options(lines []string) []Option {
	var out []Option
	seen := map[string]bool{}
	for _, l := range lines {
		m := optionRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil || seen[m[1]] {
			continue
		}
		label := strings.TrimSpace(m[2])
		if len([]rune(label)) > 48 {
			label = string([]rune(label)[:47]) + "…"
		}
		seen[m[1]] = true
		out = append(out, Option{Key: m[1], Label: label})
	}
	return out
}
