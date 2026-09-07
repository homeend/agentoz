package screen

import (
	"strings"
	"testing"
)

func TestANSIPlainTextEscaped(t *testing.T) {
	got := (ToHTML("a <b> & c\nline2 <script>x</script>"))
	want := "a &lt;b&gt; &amp; c\nline2 &lt;script&gt;x&lt;/script&gt;"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestANSIBoldAndReset(t *testing.T) {
	got := (ToHTML("\x1b[1mhi\x1b[0m there"))
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
		if got := (ToHTML(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestANSIDropsUnknownSequences(t *testing.T) {
	got := (ToHTML("\x1b[2Ja\x1b]0;title\x07b\x1b[?25lc\x1b(Bd\x1b[Kx\x1b]8;;http://x\x1b\\y"))
	if got != "abcdxy" {
		t.Errorf("got %q", got)
	}
}

func TestANSIStyleSpansCrossNewlines(t *testing.T) {
	// A styled run spanning lines stays one span (pre keeps the newline).
	got := (ToHTML("\x1b[32ma\nb\x1b[0m"))
	want := "<span style=\"color:#67c98a\">a\nb</span>"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Lines observed in a live Claude Code window, 2026-09-08.
const claudeFixture = "\x1b[38;5;174m✻\x1b[39m \x1b[38;5;174mPrecipitating… \x1b[38;5;246m(7m 5s · ↓\x1b[39m \x1b[38;5;246m5.2k tokens)\x1b[39m\n" +
	"\x1b[38;5;244m────────────────\x1b[39m\n\x1b[38;5;246m❯ \x1b[39m\n"

func TestANSIClaudeCodeFixture(t *testing.T) {
	got := (ToHTML(claudeFixture))
	if strings.Contains(got, "\x1b") {
		t.Error("escape byte leaked")
	}
	for _, want := range []string{"Precipitating…", "5.2k tokens)", "❯"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
