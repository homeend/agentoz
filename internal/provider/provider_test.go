package provider

import (
	"strings"
	"testing"
)

func reg() Registry {
	return Registry{
		"claude-code": {Command: `claude --model {model} {args} "{prompt}"`, DefaultModel: "fable-5"},
		"codex":       {Command: `codex {args} "{prompt}"`},
		"kimi":        {Command: `kimi --model {model} {args} "{prompt}"`},
	}
}

func TestRenderAllSet(t *testing.T) {
	got, err := reg().Render("kimi", "k3", "--max-turns 30", "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	want := `kimi --model 'k3' --max-turns 30 'do the thing'`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderDefaultModel(t *testing.T) {
	got, err := reg().Render("claude-code", "", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "--model 'fable-5'") {
		t.Errorf("default model not applied: %q", got)
	}
}

func TestRenderEmptyModelDropsFlag(t *testing.T) {
	got, err := reg().Render("kimi", "", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	want := `kimi 'hi'`
	if got != want {
		t.Errorf("got %q, want %q (flag+placeholder dropped)", got, want)
	}
}

func TestRenderEmptyArgsDropsToken(t *testing.T) {
	got, err := reg().Render("codex", "", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got != `codex 'hi'` {
		t.Errorf("got %q", got)
	}
}

func TestRenderPromptEscaping(t *testing.T) {
	got, err := reg().Render("codex", "", "", `it's "quoted" $HOME`)
	if err != nil {
		t.Fatal(err)
	}
	want := `codex 'it'\''s "quoted" $HOME'`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderUnknownProvider(t *testing.T) {
	if _, err := reg().Render("nope", "", "", "x"); err == nil {
		t.Fatal("want error for unknown provider")
	}
}

func TestRenderEmptyPlaceholderKeepsSubstitutedValue(t *testing.T) {
	r := Registry{"k": {Command: `k {args} {model} "{prompt}"`}}
	got, err := r.Render("k", "", "-v", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got != `k -v 'hi'` {
		t.Errorf("got %q, want %q (substituted -v must survive empty {model})", got, `k -v 'hi'`)
	}
}

func TestShellQuote(t *testing.T) {
	if got := ShellQuote(`a'b`); got != `'a'\''b'` {
		t.Errorf("ShellQuote = %q", got)
	}
}
