package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsCarryShellProviderAndPreset(t *testing.T) {
	g := Defaults()
	p, ok := g.Providers["shell"]
	if !ok || !p.IsTool() || p.Command != "${SHELL:-bash}" || p.Prompt != "" {
		t.Fatalf("shell provider = %+v (ok=%v)", p, ok)
	}
	if g.Presets["shell"].Provider != "shell" {
		t.Fatalf("shell preset = %+v", g.Presets["shell"])
	}
	if g.GgBin != "gg" {
		t.Fatalf("gg_bin default = %q", g.GgBin)
	}
	if (Provider{}).IsTool() || (Provider{Type: "agent"}).IsTool() {
		t.Fatal("agent providers must not be tools")
	}
}

func TestLoadGlobalKeepsShellUnlessOverridden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("gg_bin: /opt/gg\nproviders:\n  codex:\n    command: codex \"{prompt}\"\n"), 0o644)
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.GgBin != "/opt/gg" {
		t.Fatalf("gg_bin = %q", g.GgBin)
	}
	if _, ok := g.Providers["codex"]; !ok {
		t.Fatal("user provider lost")
	}
	if p := g.Providers["shell"]; !p.IsTool() {
		t.Fatalf("shell provider missing after load: %+v", p)
	}
	if g.Presets["shell"].Provider != "shell" {
		t.Fatal("shell preset missing after load")
	}

	os.WriteFile(path, []byte("providers:\n  shell:\n    type: tool\n    command: fish\npresets:\n  shell:\n    provider: shell\n    name: fishy\n"), 0o644)
	g, err = LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Providers["shell"].Command != "fish" || g.Presets["shell"].Name != "fishy" {
		t.Fatalf("user override lost: %+v / %+v", g.Providers["shell"], g.Presets["shell"])
	}
}

func TestSetProviderWritesType(t *testing.T) {
	out, err := SetProvider([]byte("providers:\n  x:\n    command: x\n"), "x", ProviderPatch{Type: Str("tool")})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "providers:\n  x:\n    command: x\n    type: tool\n" {
		t.Fatalf("got:\n%s", out)
	}
}
