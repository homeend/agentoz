package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsCarryBuiltinTools(t *testing.T) {
	g := Defaults()
	if g.Tools["shell"] != (Tool{Command: "${SHELL:-bash}", Terminal: true}) || g.Tools["gg"] != (Tool{Command: "{gg_bin}", Terminal: true}) {
		t.Fatalf("tools = %+v", g.Tools)
	}
	if len(g.Providers) != 0 || len(g.Presets) != 0 {
		t.Fatalf("no built-in providers/presets any more: %+v %+v", g.Providers, g.Presets)
	}
}

func TestLoadGlobalMergesToolsUserWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("tools:\n  idea:\n    command: idea64.exe {windir}\n  gg:\n    command: /opt/gg --time-track x\n    terminal: true\n"), 0o644)
	g, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Tools["idea"] != (Tool{Command: "idea64.exe {windir}"}) {
		t.Fatalf("user tool lost: %+v", g.Tools["idea"])
	}
	if g.Tools["gg"].Command != "/opt/gg --time-track x" {
		t.Fatalf("user override lost: %+v", g.Tools["gg"])
	}
	if g.Tools["shell"].Command != "${SHELL:-bash}" {
		t.Fatalf("built-in shell missing after load: %+v", g.Tools)
	}
	if _, ok := g.Providers["shell"]; ok {
		t.Fatal("shell provider must not be merged any more")
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
