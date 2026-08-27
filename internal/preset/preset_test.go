package preset

import (
	"strings"
	"testing"

	"erbrus/internal/config"
)

func TestMergeProjectOverridesGlobal(t *testing.T) {
	global := map[string]config.Preset{
		"claude": {Provider: "claude-code", Model: "fable-5"},
		"kimi":   {Provider: "kimi", Model: "k3"},
	}
	project := map[string]config.Preset{
		"claude": {Provider: "codex", Prompt: "repo conventions"},
		"extra":  {Provider: "junie"},
	}
	m := Merge(global, project)
	if m["claude"].Provider != "codex" {
		t.Error("project preset should replace global wholesale")
	}
	if m["claude"].Model != "" {
		t.Error("replace means replace: global model must not leak through")
	}
	if m["kimi"].Model != "k3" {
		t.Error("untouched global preset lost")
	}
	if m["extra"].Provider != "junie" {
		t.Error("project-only preset missing")
	}
	if global["claude"].Provider != "claude-code" {
		t.Error("Merge mutated its input")
	}
}

func TestMergeNilInputs(t *testing.T) {
	if m := Merge(nil, nil); len(m) != 0 {
		t.Errorf("want empty map, got %v", m)
	}
}

func TestResolve(t *testing.T) {
	m := map[string]config.Preset{"codex": {Provider: "codex"}}
	if _, err := Resolve("codex", m); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve("nope", m)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error should list available presets: %v", err)
	}
}
