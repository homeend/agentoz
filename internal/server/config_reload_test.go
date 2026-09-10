package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReloadConfigSwapsProvidersPresetsAndRules(t *testing.T) {
	ts, _, _ := newTestServer(t)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("providers:\n  codex:\n    command: codex\n"), 0o644)
	testSrv.SetConfigPath(cfgPath)

	// The file differs from the in-memory test config: one reload swaps
	// it in, the next one with the same file reports nothing new.
	if _, _, err := testSrv.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if changed, _, err := testSrv.ReloadConfig(); err != nil || changed {
		t.Fatalf("same file again: changed=%v err=%v", changed, err)
	}

	os.WriteFile(cfgPath, []byte(`port: 9999
gg_bin: /x/gg
providers:
  codex:
    command: codex
  agy:
    command: agy -i "{prompt}"
    screen_working: ['^spin']
presets:
  a:
    provider: agy
    model: m1
tools:
  idea:
    command: idea64.exe {windir}
`), 0o644)
	changed, restart, err := testSrv.ReloadConfig()
	if err != nil || !changed {
		t.Fatalf("reload: changed=%v err=%v", changed, err)
	}
	// data_dir may differ too (the test server points at a temp dir).
	if !strings.Contains(strings.Join(restart, ","), "port") {
		t.Fatalf("restart keys = %v, want port among them", restart)
	}
	if p, ok := testSrv.providerCfg("agy"); !ok || p.Command != `agy -i "{prompt}"` {
		t.Fatalf("provider not swapped: %+v %v", p, ok)
	}
	if p, ok := testSrv.presetCfg("a"); !ok || p.Provider != "agy" || p.Model != "m1" {
		t.Fatalf("preset not swapped: %+v %v", p, ok)
	}
	if r := testSrv.rulesFor("agy"); len(r.Working) != 1 {
		t.Fatalf("rules not recompiled: %+v", r)
	}
	if got := testSrv.ggBin(); got != "/x/gg" {
		t.Fatalf("gg_bin not swapped: %q", got)
	}
	if tl, ok := testSrv.toolCfg("idea"); !ok || tl.Command != "idea64.exe {windir}" {
		t.Fatalf("tools not swapped: %+v %v", tl, ok)
	}
	// Built-in tools survive a reload of a file that lists its own.
	if _, ok := testSrv.toolCfg("shell"); !ok {
		t.Fatal("built-in shell tool lost on reload")
	}
	if _, ok := testSrv.providerCfg("shell"); ok {
		t.Fatal("no built-in shell provider any more")
	}
	r := getPath(t, ts.URL+"/api/providers/agy")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET after reload = %d", r.StatusCode)
	}
	r.Body.Close()

	// Broken file: error, previous config kept.
	os.WriteFile(cfgPath, []byte("providers: [\n"), 0o644)
	if changed, _, err := testSrv.ReloadConfig(); err == nil || changed {
		t.Fatalf("broken file: changed=%v err=%v", changed, err)
	}
	if _, ok := testSrv.providerCfg("agy"); !ok {
		t.Fatal("broken file must keep the previous config")
	}
}

func TestConfigWatchPicksUpFileEdits(t *testing.T) {
	ts, _, _ := newTestServer(t)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("providers:\n  codex:\n    command: codex\n"), 0o644)
	testSrv.SetConfigPath(cfgPath)
	stop := make(chan struct{})
	defer close(stop)
	testSrv.StartConfigWatch(10*time.Millisecond, stop)

	// mtime granularity can be coarse; a size change is enough for the stamp.
	time.Sleep(30 * time.Millisecond)
	os.WriteFile(cfgPath, []byte("providers:\n  codex:\n    command: codex\n  junie:\n    command: junie \"{prompt}\"\n"), 0o644)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r := getPath(t, ts.URL+"/api/providers/junie")
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watch did not reload the edited file within 3s")
}

// TestReloadAppliesDriverShellToolUnlessUserDefinesShell: the built-in
// shell tool's command comes from the driver's ShellTool() unless the
// user's config defines its own "shell" tool, in which case the user wins.
func TestReloadAppliesDriverShellToolUnlessUserDefinesShell(t *testing.T) {
	ts, _, _ := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetDriver(&fakeDriver{fakeSpawner: fs, name: "wezterm"}, "/abs/erbrus", ts.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("providers:\n  codex:\n    command: codex\n"), 0o644)
	testSrv.SetConfigPath(cfgPath)
	if _, _, err := testSrv.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if tl, ok := testSrv.toolCfg("shell"); !ok || tl.Command != "powershell" {
		t.Fatalf("shell tool = %+v, %v; want driver's ShellTool() (powershell)", tl, ok)
	}
	// Same file again: the driver's shell override must not make an
	// unrelated reload look "changed" forever (deviation #2 in the task
	// report — shellToolOverride is applied to next.Tools before the
	// DeepEqual comparison, not only after the swap).
	if changed, _, err := testSrv.ReloadConfig(); err != nil || changed {
		t.Fatalf("same file again under a non-posix driver: changed=%v err=%v", changed, err)
	}

	// The user defines their own shell tool: it wins over the driver.
	os.WriteFile(cfgPath, []byte("providers:\n  codex:\n    command: codex\ntools:\n  shell:\n    command: bash\n    terminal: true\n"), 0o644)
	if _, _, err := testSrv.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if tl, ok := testSrv.toolCfg("shell"); !ok || tl.Command != "bash" {
		t.Fatalf("shell tool = %+v, %v; want the user's bash", tl, ok)
	}
}

func getPath(t *testing.T, url string) *http.Response {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
