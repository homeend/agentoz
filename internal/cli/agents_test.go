package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/agentskill"
)

func agentsTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".kimi-code"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "skills", "erbrus"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "skills", "erbrus", "SKILL.md"), []byte("---\n---\n<!-- erbrus:erbrus:v0 -->\n"), 0o644)
	old, oldLook := agentsHomeDir, agentsLookPath
	agentsHomeDir = func() (string, error) { return home, nil }
	agentsLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { agentsHomeDir, agentsLookPath = old, oldLook })
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfg, []byte("# mine\nproviders:\n  claude-code:\n    command: claude\n"), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfg)
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:1") // no server: file path
	return home
}

func TestAgentsList(t *testing.T) {
	agentsTestHome(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"agents", "list"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	s := out.String()
	for _, want := range []string{"Claude Code", "outdated", "provider: configured", "Kimi Code", "new", "will be added"} {
		if !strings.Contains(s, want) {
			t.Errorf("list missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Junie") {
		t.Errorf("undetected agent listed:\n%s", s)
	}
}

func TestAgentsSetupByIDWritesSkillAndProvider(t *testing.T) {
	home := agentsTestHome(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"agents", "setup", "--agents", "kimi"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "skills", "erbrus", "SKILL.md"))
	if err != nil || string(b) != agentskill.SkillFile() {
		t.Fatalf("skill not installed: %v", err)
	}
	doc, _ := os.ReadFile(os.Getenv("ERBRUS_CONFIG"))
	for _, want := range []string{"# mine", "  claude-code:", "  kimi:", "prompt: paste", "screen_waiting:"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("config missing %q:\n%s", want, doc)
		}
	}
	if !strings.Contains(out.String(), "restart erbrus serve") {
		t.Errorf("no-server path must say to restart:\n%s", out.String())
	}
	// Claude untouched (not selected); provider kept as is.
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "erbrus", "SKILL.md")); !strings.Contains(string(b), "v0") {
		t.Error("unselected agent was touched")
	}
	if code := Run([]string{"agents", "setup", "--agents", "nope"}, &out, &errOut); code != 2 {
		t.Errorf("unknown id exit = %d", code)
	}
}

func TestAgentsSetupUpdateOnlyRefreshesInstalled(t *testing.T) {
	home := agentsTestHome(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"agents", "setup", "--update"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", "skills", "erbrus", "SKILL.md")); string(b) != agentskill.SkillFile() {
		t.Error("outdated claude skill not refreshed")
	}
	if _, err := os.Stat(filepath.Join(home, ".kimi-code", "skills", "erbrus", "SKILL.md")); err == nil {
		t.Error("--update must not install new ones")
	}
}

func TestAgentsSetupInteractive(t *testing.T) {
	home := agentsTestHome(t)
	var out, errOut bytes.Buffer
	if code := runAgents([]string{"setup"}, strings.NewReader("2\n"), &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".kimi-code", "skills", "erbrus", "SKILL.md")); err != nil {
		t.Error("selection 2 (kimi) not installed")
	}
	if code := runAgents([]string{"setup"}, strings.NewReader("q\n"), &out, &errOut); code != 0 {
		t.Errorf("quit exit = %d", code)
	}
	if code := runAgents([]string{"setup"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Errorf("no input exit = %d", code)
	}
}

func TestParseSelection(t *testing.T) {
	checked := []bool{true, false, true}
	cases := map[string][]int{"": {0, 2}, "a": {0, 1, 2}, "2": {1}, "1,3": {0, 2}, "q": nil}
	for in, want := range cases {
		got, err := parseSelection(in, 3, checked)
		if err != nil || len(got) != len(want) {
			t.Errorf("%q: %v %v", in, got, err)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: %v", in, got)
			}
		}
	}
	if _, err := parseSelection("9", 3, checked); err == nil {
		t.Error("out of range accepted")
	}
}
