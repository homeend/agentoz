package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	g := Defaults()
	if g.Port != 7420 {
		t.Errorf("Port = %d, want 7420", g.Port)
	}
	if g.Terminal != "tmux" {
		t.Errorf("Terminal = %q, want tmux", g.Terminal)
	}
	if g.SessionPattern != "erbrus-{project}" {
		t.Errorf("SessionPattern = %q", g.SessionPattern)
	}
	if g.WtBin != "wt" {
		t.Errorf("WtBin = %q, want wt", g.WtBin)
	}
}

func TestLoadGlobalMissingFileGivesDefaults(t *testing.T) {
	g, err := LoadGlobal(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Port != 7420 {
		t.Errorf("Port = %d, want default 7420", g.Port)
	}
}

func TestLoadGlobalMergesOverDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	write(t, p, "port: 9999\nproviders:\n  codex:\n    command: 'codex {args} \"{prompt}\"'\npresets:\n  codex:\n    provider: codex\n")
	g, err := LoadGlobal(p)
	if err != nil {
		t.Fatal(err)
	}
	if g.Port != 9999 {
		t.Errorf("Port = %d, want 9999", g.Port)
	}
	if g.Terminal != "tmux" {
		t.Errorf("Terminal = %q, want default kept", g.Terminal)
	}
	if g.Providers["codex"].Command == "" {
		t.Error("provider codex not loaded")
	}
	if g.Presets["codex"].Provider != "codex" {
		t.Error("preset codex not loaded")
	}
}

func TestLoadGlobalBadYAMLErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	write(t, p, "port: [broken")
	if _, err := LoadGlobal(p); err == nil {
		t.Fatal("want error for invalid YAML")
	}
}

func TestLoadRepo(t *testing.T) {
	t.Setenv("ERBRUS_HOME", t.TempDir())
	root := t.TempDir()
	write(t, filepath.Join(root, ".erbrus.yaml"),
		"session: my-sess\nchannel_per_worktree: false\npresets:\n  claude:\n    provider: claude-code\n")
	r, legacy, err := LoadRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if !legacy {
		t.Error("legacy flag should be true when reading from legacy location")
	}
	if r.Session != "my-sess" {
		t.Errorf("Session = %q", r.Session)
	}
	if r.ChannelPerWorktree == nil || *r.ChannelPerWorktree != false {
		t.Error("ChannelPerWorktree should be explicit false")
	}
	if r.Presets["claude"].Provider != "claude-code" {
		t.Error("repo preset not loaded")
	}
}

func TestLoadRepoMissingIsZero(t *testing.T) {
	t.Setenv("ERBRUS_HOME", t.TempDir())
	r, legacy, err := LoadRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Error("legacy flag should be false when no config found")
	}
	if r.Session != "" || r.ChannelPerWorktree != nil {
		t.Errorf("want zero Repo, got %+v", r)
	}
}

func TestGlobalPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	if got := GlobalPath(); got != "/x/cfg/erbrus/config.yaml" {
		t.Errorf("GlobalPath() = %q", got)
	}
}

func TestResolvedDataDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/x/data")
	g := Defaults()
	if got := g.ResolvedDataDir(); got != "/x/data/erbrus" {
		t.Errorf("ResolvedDataDir() = %q", got)
	}
	g.DataDir = "~/custom"
	home, _ := os.UserHomeDir()
	if got := g.ResolvedDataDir(); got != filepath.Join(home, "custom") {
		t.Errorf("ResolvedDataDir() = %q", got)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/a/b"); got != filepath.Join(home, "a/b") {
		t.Errorf("ExpandHome = %q", got)
	}
	if got := ExpandHome("/abs/x"); got != "/abs/x" {
		t.Errorf("ExpandHome mangled absolute path: %q", got)
	}
	if !strings.HasPrefix(ExpandHome("~"), home) {
		t.Error("bare ~ not expanded")
	}
}

func TestEncodeRepoPath(t *testing.T) {
	if got := EncodeRepoPath("/home/homeend/git-focus"); got != "-home-homeend-git-focus" {
		t.Errorf("EncodeRepoPath = %q", got)
	}
	if got := EncodeRepoPath("/a/b/c"); got != "-a-b-c" {
		t.Errorf("EncodeRepoPath = %q", got)
	}
}

func TestRepoConfigPathUsesErbrusHome(t *testing.T) {
	t.Setenv("ERBRUS_HOME", "/x/.erbrus")
	if got := RepoConfigPath("/a/b"); got != "/x/.erbrus/repos/-a-b/.erbrus.yaml" {
		t.Errorf("RepoConfigPath = %q", got)
	}
}

func TestLoadRepoNewLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ERBRUS_HOME", home)
	root := t.TempDir()
	cfgPath := RepoConfigPath(root)
	write(t, cfgPath, "session: from-new-location\n")
	r, legacy, err := LoadRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Error("new-location read must report legacy=false")
	}
	if r.Session != "from-new-location" {
		t.Errorf("Session = %q", r.Session)
	}
}

func TestLoadRepoLegacyFallback(t *testing.T) {
	t.Setenv("ERBRUS_HOME", t.TempDir())
	root := t.TempDir()
	write(t, filepath.Join(root, ".erbrus.yaml"), "session: from-legacy\n")
	r, legacy, err := LoadRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if !legacy {
		t.Error("legacy read must report legacy=true")
	}
	if r.Session != "from-legacy" {
		t.Errorf("Session = %q", r.Session)
	}
}

func TestLoadRepoNewLocationWinsOverLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ERBRUS_HOME", home)
	root := t.TempDir()
	write(t, RepoConfigPath(root), "session: new\n")
	write(t, filepath.Join(root, ".erbrus.yaml"), "session: old\n")
	r, legacy, _ := LoadRepo(root)
	if legacy || r.Session != "new" {
		t.Errorf("want new location to win: legacy=%v session=%q", legacy, r.Session)
	}
}

func TestLoadRepoMissingBothIsZero(t *testing.T) {
	t.Setenv("ERBRUS_HOME", t.TempDir())
	r, legacy, err := LoadRepo(t.TempDir())
	if err != nil || legacy || r.Session != "" {
		t.Errorf("want zero Repo: %+v legacy=%v err=%v", r, legacy, err)
	}
}
