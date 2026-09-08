package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const rulesDoc = `# erbrus global settings
port: 7420

providers:
  claude-code:
    command: 'claude {args} "{prompt}"'   # keep this comment
    default_model: sonnet
  kimi:
    command: kimi
    screen_question: ['OLD']

presets:
  claude: {provider: claude-code}
`

func TestSetProviderScreenRulesKeepsEverythingElse(t *testing.T) {
	out, err := SetProviderScreenRules([]byte(rulesDoc), "kimi", []string{`Thinking \(`}, nil, []string{"CONFIRM", `\(y/n\)`})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"# erbrus global settings", "# keep this comment", "port: 7420", "default_model: sonnet",
		"screen_working: ['Thinking \\(']", "screen_question: ['CONFIRM', '\\(y/n\\)']", "presets:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OLD") || strings.Contains(got, "screen_waiting") {
		t.Errorf("stale or empty keys left in:\n%s", got)
	}
	var g Global
	if err := yaml.Unmarshal(out, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Providers["kimi"].ScreenQuestion) != 2 || g.Providers["kimi"].ScreenWorking[0] != `Thinking \(` {
		t.Fatalf("round trip: %+v", g.Providers["kimi"])
	}
	// All empty removes every screen key (back to built-ins).
	out2, _ := SetProviderScreenRules(out, "kimi", nil, nil, nil)
	if strings.Contains(string(out2), "screen_") {
		t.Errorf("keys not removed:\n%s", out2)
	}
	if _, err := SetProviderScreenRules([]byte(rulesDoc), "nope", nil, nil, nil); err == nil {
		t.Error("unknown provider accepted")
	}
}
