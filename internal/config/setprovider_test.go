package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseGlobalDoc(t *testing.T, doc []byte) Global {
	t.Helper()
	g := Defaults()
	if err := yaml.Unmarshal(doc, &g); err != nil {
		t.Fatalf("round trip: %v\n%s", err, doc)
	}
	return g
}

func TestSetProviderCreatesAndUpdates(t *testing.T) {
	doc := []byte("# mine\nport: 7420\n")
	out, err := SetProvider(doc, "kimi", ProviderPatch{Command: Str(`kimi --yolo --model {model} {args}`), Prompt: Str("paste"),
		Waiting: List([]string{`^│ >[^\n]*\n╰`})})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# mine", "port: 7420", "providers:", "  kimi:", "    command: kimi --yolo --model {model} {args}", "    prompt: paste", `screen_waiting: ['^│ >[^\n]*\n╰']`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	g := parseGlobalDoc(t, out)
	if g.Providers["kimi"].Prompt != "paste" || len(g.Providers["kimi"].ScreenWaiting) != 1 {
		t.Fatalf("round trip: %+v", g.Providers["kimi"])
	}
	// Update one field, keep the rest; clear a list with an empty one.
	out2, err := SetProvider(out, "kimi", ProviderPatch{DefaultModel: Str("kimi-code/k3"), Waiting: List(nil)})
	if err != nil {
		t.Fatal(err)
	}
	p := parseGlobalDoc(t, out2).Providers["kimi"]
	if p.Command == "" || p.Prompt != "paste" || p.DefaultModel != "kimi-code/k3" || len(p.ScreenWaiting) != 0 {
		t.Fatalf("update: %+v", p)
	}
	if strings.Contains(string(out2), "screen_waiting") || !strings.Contains(string(out2), "# mine") {
		t.Fatalf("cleared key or comment wrong:\n%s", out2)
	}
	// Existing document with comments: entry added, nothing else moved.
	out3, err := SetProvider([]byte(rulesDoc), "junie", ProviderPatch{Command: Str(`junie {args} "{prompt}"`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep this comment", "default_model: sonnet", "  junie:", `command: junie {args} "{prompt}"`, "presets:"} {
		if !strings.Contains(string(out3), want) {
			t.Errorf("missing %q:\n%s", want, out3)
		}
	}
	if _, err := SetProvider([]byte("providers: [a, b]\n"), "x", ProviderPatch{Command: Str("x")}); err == nil {
		t.Error("providers that is not a mapping must error")
	}
}

func TestSetProviderScreenRulesStillRequiresProvider(t *testing.T) {
	if _, err := SetProviderScreenRules([]byte("providers:\n  x:\n    command: x\n"), "nope", nil, nil, nil); err == nil {
		t.Fatal("unknown provider must error")
	}
}
