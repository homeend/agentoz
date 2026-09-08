package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/agentskill"
	"erbrus/internal/config"
	"erbrus/internal/provider"
)

func fakeLook(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestBuiltinsRenderAndHaveSkillPaths(t *testing.T) {
	ids := map[string]bool{}
	for _, a := range Builtins() {
		ids[a.ID] = true
		if !strings.HasSuffix(a.Skill, "/skills/erbrus/SKILL.md") || !strings.HasPrefix(a.Skill, "~/") {
			t.Errorf("%s: skill path %q", a.ID, a.Skill)
		}
		if a.Provider.DefaultModel != "" {
			t.Errorf("%s: registry must not pin a default model", a.ID)
		}
		reg := provider.Registry{a.ID: a.Provider}
		if _, err := reg.Render(a.ID, "m", "", "hi"); err != nil {
			t.Errorf("%s: template does not render: %v", a.ID, err)
		}
		if _, err := reg.Render(a.ID, "", "", ""); err != nil {
			t.Errorf("%s: empty render: %v", a.ID, err)
		}
	}
	for _, want := range []string{"claude-code", "codex", "kimi", "junie", "antigravity"} {
		if !ids[want] {
			t.Errorf("missing %s", want)
		}
	}
	if len(ids) != 5 {
		t.Errorf("registry has %d entries", len(ids))
	}
}

func TestKimiIsPasteWithRules(t *testing.T) {
	for _, a := range Builtins() {
		if a.ID != "kimi" {
			continue
		}
		if a.Provider.Prompt != "paste" || strings.Contains(a.Provider.Command, "{prompt}") {
			t.Errorf("kimi must paste: %+v", a.Provider)
		}
		if len(a.Provider.ScreenWaiting) == 0 || len(a.Provider.ScreenQuestion) == 0 || len(a.Provider.ScreenWorking) == 0 {
			t.Errorf("kimi rules missing: %+v", a.Provider)
		}
	}
}

func TestDetect(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude", "skills", "erbrus"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "skills", "erbrus", "SKILL.md"), []byte("---\nname: erbrus\n---\n<!-- erbrus:erbrus:v0 -->\nold"), 0o644)
	os.MkdirAll(filepath.Join(home, ".kimi-code"), 0o755)
	os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o755)
	os.MkdirAll(filepath.Join(home, ".gemini", "config", "skills", "erbrus"), 0o755)
	os.WriteFile(filepath.Join(home, ".gemini", "config", "skills", "erbrus", "SKILL.md"), []byte(agentskill.SkillFile()), 0o644)
	cfg := config.Defaults()
	cfg.Providers = map[string]config.Provider{"claude-code": {Command: "claude"}}

	dets := Detect(home, fakeLook("claude", "junie"), cfg)
	byID := map[string]Detection{}
	for _, d := range dets {
		byID[d.Agent.ID] = d
	}
	if _, ok := byID["codex"]; ok {
		t.Error("codex: no home, no binary → not detected")
	}
	c := byID["claude-code"]
	if c.Status != StatusOutdated || !c.Configured || c.BinaryPath == "" || c.SkillPath != filepath.Join(home, ".claude", "skills", "erbrus", "SKILL.md") {
		t.Errorf("claude: %+v", c)
	}
	if k := byID["kimi"]; k.Status != StatusNew || k.Configured || k.BinaryPath != "" {
		t.Errorf("kimi (home only): %+v", k)
	}
	if j := byID["junie"]; j.Status != StatusNew || j.BinaryPath == "" {
		t.Errorf("junie (binary only): %+v", j)
	}
	if a := byID["antigravity"]; a.Status != StatusUpToDate {
		t.Errorf("antigravity: %+v", a)
	}
	if len(Detect("", fakeLook(), cfg)) != 0 {
		t.Error("empty home and nothing on PATH must detect nothing")
	}
}

func TestInstallWritesAndOverwrites(t *testing.T) {
	home := t.TempDir()
	d := Detection{Agent: Builtins()[0], SkillPath: filepath.Join(home, "x", "skills", "erbrus", "SKILL.md")}
	if err := Install(d); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(d.SkillPath)
	if string(b) != agentskill.SkillFile() {
		t.Fatal("installed content differs from the rendered skill")
	}
	os.WriteFile(d.SkillPath, []byte("stale"), 0o644)
	Install(d)
	if b, _ := os.ReadFile(d.SkillPath); string(b) != agentskill.SkillFile() {
		t.Fatal("stale file not overwritten")
	}
}

func TestStatusStrings(t *testing.T) {
	if StatusNew.String() != "new" || StatusOutdated.String() != "outdated" || StatusUpToDate.String() != "up to date" {
		t.Error("status strings")
	}
	if StatusNew.Checked() || !StatusOutdated.Checked() || !StatusUpToDate.Checked() {
		t.Error("checked defaults: existing installs are checked, new ones are not")
	}
}
