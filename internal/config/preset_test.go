package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSetPresetCreatesAndUpdates(t *testing.T) {
	doc := []byte("# mine\nproviders:\n  codex:\n    command: codex\n")
	out, err := SetPreset(doc, "agy", PresetPatch{Provider: Str("antigravity")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# mine", "providers:", "presets:", "  agy:", "    provider: antigravity"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	g, err := parseGlobal(out)
	if err != nil || g.Presets["agy"].Provider != "antigravity" {
		t.Fatalf("round trip: %v %+v", err, g.Presets)
	}

	// Update keeps the provider, sets a model; "" removes it again.
	out, err = SetPreset(out, "agy", PresetPatch{Model: Str("gemini-3-pro")})
	if err != nil {
		t.Fatal(err)
	}
	g, _ = parseGlobal(out)
	if g.Presets["agy"].Provider != "antigravity" || g.Presets["agy"].Model != "gemini-3-pro" {
		t.Fatalf("after model: %+v", g.Presets["agy"])
	}
	out, _ = SetPreset(out, "agy", PresetPatch{Model: Str("")})
	if strings.Contains(string(out), "model:") {
		t.Fatalf("empty model must remove the key:\n%s", out)
	}

	// A non-mapping presets section is refused.
	if _, err := SetPreset([]byte("presets: 3\n"), "x", PresetPatch{Provider: Str("p")}); err == nil {
		t.Fatal("expected error for scalar presets")
	}
}

func parseGlobal(doc []byte) (Global, error) {
	g := Defaults()
	err := yaml.Unmarshal(doc, &g)
	return g, err
}
