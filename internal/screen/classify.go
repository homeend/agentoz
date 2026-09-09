package screen

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
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
		{`Working \(\d+`, `(?i)esc to interrupt`},
		// The input box is a › line (placeholder "Ask Codex to do anything"
		// or typed text) directly above the status line "<model> <effort>
		// · <cwd>". A user message echoed in the transcript also starts
		// with › but is followed by other text. Captured live 2026-09-08
		// (codex 0.153.4).
		{`^›[^\n]*\n[^\n]*· /`},
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

// Option is one choice of a dialog: numbered ("› 1. Try new model", Key
// is the digit to press) or cursor-style ("> Yes, I trust this folder" /
// "  No, exit", Key is "pick:<i>" and the server walks the cursor there).
type Option struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Pick    bool   `json:"pick,omitempty"`    // cursor-style: select by moving, then Enter
	Current bool   `json:"current,omitempty"` // the cursor is on this one now
}

// DialogOptions: numbered options when the dialog has them, else the
// cursor-style ones read from the untrimmed screen (indentation matters
// there). lines are the trimmed tail used for classification.
func DialogOptions(raw string, lines []string) []Option {
	if o := Options(lines); len(o) > 0 {
		return o
	}
	return CursorOptions(raw)
}

var (
	cursorRe = regexp.MustCompile(`^(\s*)([>❯›●◉▸▶→])(\s+)(\S.*)$`)
	// hintRe: the navigation help under a cursor list ("↑/↓ Navigate ·
	// enter Confirm", "↑↓ navigate · Enter select · Esc exit"), never a choice.
	hintRe = regexp.MustCompile(`[↑↓]|(?i)\bnavigate\b|\bconfirm\b|\besc\b`)
)

// CursorOptions reads a dialog whose choices are a column of lines with a
// cursor marker on the current one (Antigravity, Kimi). The cursor line
// fixes the text column; the contiguous lines above and below whose text
// starts in that column are the other choices. A lone marker line (an
// input prompt) is not a dialog. At most 9 choices, labels cut at 48 runes.
func CursorOptions(raw string) []Option {
	lines := strings.Split(Strip(raw), "\n")
	cur, col := -1, 0
	var curLabel string
	for i := len(lines) - 1; i >= 0; i-- {
		m := cursorRe.FindStringSubmatch(strings.TrimRight(lines[i], " \t"))
		if m == nil || hintRe.MatchString(m[4]) {
			continue
		}
		cur = i
		col = utf8.RuneCountInString(m[1]) + 1 + utf8.RuneCountInString(m[3])
		curLabel = strings.TrimSpace(m[4])
		break
	}
	if cur < 0 {
		return nil
	}
	sibling := func(l string) (string, bool) {
		l = strings.TrimRight(l, " \t")
		n := 0
		for _, r := range l {
			if r != ' ' && r != '\t' {
				break
			}
			n++
		}
		if n != col || n == utf8.RuneCountInString(l) {
			return "", false
		}
		text := strings.TrimSpace(l)
		if hintRe.MatchString(text) || cursorRe.MatchString(text) {
			return "", false
		}
		return text, true
	}
	start, end := cur, cur
	for start > 0 {
		if _, ok := sibling(lines[start-1]); !ok {
			break
		}
		start--
	}
	for end+1 < len(lines) {
		if _, ok := sibling(lines[end+1]); !ok {
			break
		}
		end++
	}
	if end-start < 1 {
		return nil
	}
	var out []Option
	for i := start; i <= end && len(out) < 9; i++ {
		label := curLabel
		if i != cur {
			label, _ = sibling(lines[i])
		}
		if n := []rune(label); len(n) > 48 {
			label = string(n[:47]) + "…"
		}
		out = append(out, Option{Key: fmt.Sprintf("pick:%d", len(out)), Label: label, Pick: true, Current: i == cur})
	}
	return out
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
