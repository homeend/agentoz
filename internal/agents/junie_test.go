package agents

import (
	"testing"

	"erbrus/internal/screen"
)

// Screens captured live from junie 26.9.7 (2026-09-09), trimmed the way
// the classifier sees them.
var junieScreens = []struct {
	want screen.State
	raw  string
}{
	{screen.Working, "Post a report as your FINAL step — the next agent picks up from it.\n" +
		"Count from one to thirty in words, one per line, slowly. Do not modify files.\n" +
		"⠹ Thinking... esc to stop\n" +
		"Tip: Paste images into your prompt to share visual context\n" +
		">   Type your prompt...\n" +
		"~ juniewd  ⚑ Brave auto ctrl + b  ⌘ Gemini 3.7 Flash JetBrains AI  ◐ Medium effort\n"},
	{screen.Waiting, "• Executed slow word-by-word sequence counting from \"one\" to \"thirty\" in terminal output.\n" +
		"• Successfully transmitted completion report message via erbrus CLI.\n" +
		">   Type your prompt...\n" +
		"~ juniewd (master)  ⚑ Brave auto ctrl + b  ⌘ Gemini 3.7 Flash JetBrains AI  ◐ Medium effort  2% context used\n" +
		"0.02 credits spent\n"},
	{screen.Question, "Junie needs your trust decision\n" +
		"Trusting a project loads its MCP servers, skills, commands, and Junie configuration.\n" +
		"Project: /tmp/x/juniewd\n" +
		"→ Trust this project\n" +
		"Trust all projects in /tmp/x\n" +
		"Keep untrusted\n"},
	{screen.Question, "! Trying to run /abs/erbrus msg send --report 'You selected: Sweet Rolls.'\n" +
		"───────────\n" +
		"Allow running this command?\n" +
		"Allow once\n" +
		"→ Always allow (\"erbrus msg send *\")\n" +
		"Deny\n" +
		">  Or reject with a reason\n"},
	{screen.Question, "Which dessert would you prefer?\n" +
		"→ ○ Fresh fruits (recommended)\n" +
		"Maximizes net well-being by providing natural sweetness.\n" +
		"○ Ice cream\n" +
		"○ Sweet rolls\n" +
		">  Or type your own answer…\n" +
		"───────────\n" +
		"Chat about this\n" +
		"space to select\n" +
		"esc to cancel\n"},
}

func TestJunieRulesClassifyLiveCaptures(t *testing.T) {
	var j Agent
	for _, a := range Builtins() {
		if a.ID == "junie" {
			j = a
		}
	}
	if j.Provider.Prompt != "paste" || j.Provider.Command != "junie --brave --model {model} {args}" {
		t.Fatalf("junie provider = %+v", j.Provider)
	}
	rules, err := screen.Compile(j.Provider.ScreenWorking, j.Provider.ScreenWaiting, j.Provider.ScreenQuestion)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range junieScreens {
		if got := screen.Classify(rules, screen.Tail(c.raw, 15)); got != c.want {
			t.Errorf("screen %d: classified as %q, want %q", i, got, c.want)
		}
	}
}
