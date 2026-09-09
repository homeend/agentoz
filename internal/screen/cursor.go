package screen

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Cursor-style dialogs: a column of choices with a marker on the current
// one. Two layouts seen live (2026-09-09):
//
//	> Yes, I trust this folder        → Allow once                   (agy, junie)
//	  No, exit                          Always allow ("erbrus …")
//	  ↑/↓ Navigate · enter Confirm      Deny
//
//	→ ○ Fresh fruits (recommended)                                   (junie ask-user)
//	    Maximizes net well-being …        ← description, deeper indent
//	  ○ Ice cream
//	    Delivers an immediate surge …
//
// The cursor line fixes the text column. Plain lists take the contiguous
// lines whose text starts in that column; radio lists (the text starts
// with a radio glyph) take every glyph line in the block and skip the
// deeper-indented descriptions. Question lines ("…?", "…:"), navigation
// hints and input prompts never count.
var (
	cursorRe = regexp.MustCompile(`^(\s*)([>❯›●◉▸▶→])(\s+)(\S.*)$`)
	// hintRe: the navigation help under a cursor list ("↑/↓ Navigate ·
	// enter Confirm", "↑↓ navigate · Enter select · Esc exit"), never a choice.
	hintRe  = regexp.MustCompile(`[↑↓]|(?i)\bnavigate\b|\bconfirm\b|\besc\b|\bspace to select\b`)
	radioRe = regexp.MustCompile(`^[○●◉◯□■☐☑]\s+(\S.*)$`)
)

// CursorOptions reads a cursor-style dialog from the untrimmed screen
// (indentation matters). A lone marker line (an input prompt) is not a
// dialog. At most 9 choices, labels cut at 48 runes.
func CursorOptions(raw string) []Option {
	lines := strings.Split(Strip(raw), "\n")
	// Bottom-up: the input prompt under a dialog ("> Or type your own
	// answer…") looks like a cursor line too but has no siblings, so the
	// first candidate that yields a list wins.
	for i := len(lines) - 1; i >= 0; i-- {
		m := cursorRe.FindStringSubmatch(strings.TrimRight(lines[i], " \t"))
		if m == nil || hintRe.MatchString(m[4]) {
			continue
		}
		col := utf8.RuneCountInString(m[1]) + 1 + utf8.RuneCountInString(m[3])
		if out := optionsAround(lines, i, col, strings.TrimSpace(m[4])); out != nil {
			return out
		}
	}
	return nil
}

// optionsAround collects the choices around cursor line cur, whose text
// starts at column col; nil when it has no siblings.
func optionsAround(lines []string, cur, col int, curLabel string) []Option {
	radio := radioRe.MatchString(curLabel)
	if radio {
		curLabel = radioRe.FindStringSubmatch(curLabel)[1]
	}
	// indentOf: leading-space count and the trimmed text ("" for blank).
	indentOf := func(l string) (int, string) {
		l = strings.TrimRight(l, " \t")
		n := 0
		for _, r := range l {
			if r != ' ' && r != '\t' {
				break
			}
			n++
		}
		return n, strings.TrimSpace(l)
	}
	// choice: is line i another choice? skip: neither a choice nor the
	// end of the block (a radio description).
	choice := func(i int) (label string, ok, skip bool) {
		n, text := indentOf(lines[i])
		if text == "" || hintRe.MatchString(text) || cursorRe.MatchString(text) {
			return "", false, false
		}
		if radio {
			if m := radioRe.FindStringSubmatch(text); m != nil && n == col {
				return m[1], true, false
			}
			return "", false, n > col
		}
		if n != col || strings.HasSuffix(text, "?") || strings.HasSuffix(text, ":") {
			return "", false, false
		}
		return text, true, false
	}
	type item struct {
		label   string
		current bool
	}
	var above, below []item
	for i := cur - 1; i >= 0; i-- {
		label, ok, skip := choice(i)
		if ok {
			above = append([]item{{label: label}}, above...)
			continue
		}
		if !skip {
			break
		}
	}
	for i := cur + 1; i < len(lines); i++ {
		label, ok, skip := choice(i)
		if ok {
			below = append(below, item{label: label})
			continue
		}
		if !skip {
			break
		}
	}
	all := append(append(above, item{label: curLabel, current: true}), below...)
	if len(all) < 2 {
		return nil
	}
	var out []Option
	for _, it := range all {
		if len(out) == 9 {
			break
		}
		label := it.label
		if n := []rune(label); len(n) > 48 {
			label = string(n[:47]) + "…"
		}
		out = append(out, Option{Key: fmt.Sprintf("pick:%d", len(out)), Label: label, Pick: true, Current: it.current})
	}
	return out
}
