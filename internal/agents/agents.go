// Package agents knows the AI coding CLIs erbrus can drive: where each
// keeps user-level skills, how to spot it, and the provider entry that
// makes it usable. The registry is code — supporting a new agent is one
// entry here, never a runtime definition.
package agents

import (
	"os"
	"path/filepath"
	"strings"

	"erbrus/internal/agentskill"
	"erbrus/internal/config"
)

// Agent is one registry entry. Home and Skill are "~/"-relative.
type Agent struct {
	ID       string // provider name in config.yaml
	Label    string
	Binary   string // looked up on PATH
	Home     string // its presence means "installed"
	Skill    string // where SKILL.md goes
	Provider config.Provider
	Note     string
}

// Builtins is the registry. Defaults never pin a model: an empty {model}
// drops the flag and the CLI uses its own default.
func Builtins() []Agent {
	return []Agent{
		{ID: "claude-code", Label: "Claude Code", Binary: "claude", Home: "~/.claude", Skill: "~/.claude/skills/erbrus/SKILL.md",
			Provider: config.Provider{Command: `claude --model {model} {args} "{prompt}"`}},
		// Codex reads $CODEX_HOME/skills/<name>/SKILL.md (it created
		// ~/.codex/skills/.system itself; its binary carries a skills loader).
		{ID: "codex", Label: "Codex", Binary: "codex", Home: "~/.codex", Skill: "~/.codex/skills/erbrus/SKILL.md",
			Provider: config.Provider{Command: `codex --model {model} {args} "{prompt}"`}},
		// Kimi's interactive mode takes no initial prompt (-p is
		// non-interactive), so the prompt is pasted once the input box is
		// up. Rules from a live capture 2026-09-08; the working rule saw
		// only the retry spinner (the API was down), so it is a best guess.
		{ID: "kimi", Label: "Kimi Code", Binary: "kimi", Home: "~/.kimi-code", Skill: "~/.kimi-code/skills/erbrus/SKILL.md",
			Provider: config.Provider{
				Command: `kimi --yolo --model {model} {args}`, Prompt: "paste",
				ScreenWorking:  []string{`^[🌑🌒🌓🌔🌕🌖🌗🌘] `, `Retrying \(\d+/\d+\)`},
				ScreenWaiting:  []string{`^│ >[^\n]*\n╰`},
				ScreenQuestion: []string{`Trust this folder\?`, `Enter select`, `Esc exit`},
			}, Note: "prompt by paste"},
		{ID: "junie", Label: "Junie", Binary: "junie", Home: "~/.junie", Skill: "~/.junie/skills/erbrus/SKILL.md",
			Provider: config.Provider{Command: `junie {args} "{prompt}"`}},
		// Antigravity CLI (agy 1.1.4): -i runs an initial prompt
		// interactively; skills live under ~/.gemini/config/skills; detect
		// its own home, not plain ~/.gemini (gemini-cli creates that too).
		{ID: "antigravity", Label: "Antigravity", Binary: "agy", Home: "~/.gemini/antigravity-cli", Skill: "~/.gemini/config/skills/erbrus/SKILL.md",
			Provider: config.Provider{Command: `agy --dangerously-skip-permissions --model "{model}" {args} -i "{prompt}"`}},
	}
}

type Status int

const (
	StatusNew Status = iota
	StatusOutdated
	StatusUpToDate
)

func (s Status) String() string {
	switch s {
	case StatusOutdated:
		return "outdated"
	case StatusUpToDate:
		return "up to date"
	}
	return "new"
}

// Checked is the default selection: existing installs refresh, first
// installs are opt-in (same rule as gg init).
func (s Status) Checked() bool { return s != StatusNew }

// Detection is one agent found on this machine.
type Detection struct {
	Agent      Agent
	BinaryPath string // "" when not on PATH
	SkillPath  string // absolute
	Status     Status
	Configured bool // providers.<ID> exists in cfg
}

func resolve(p, home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}

// Detect returns registry entries whose home dir exists or whose binary
// is on PATH. homeDir "" skips home probing (tests never see the real one).
func Detect(homeDir string, lookPath func(string) (string, error), cfg config.Global) []Detection {
	var out []Detection
	for _, a := range Builtins() {
		bin, _ := lookPath(a.Binary)
		home := resolve(a.Home, homeDir)
		homeOK := false
		if home != "" {
			if _, err := os.Stat(home); err == nil {
				homeOK = true
			}
		}
		if bin == "" && !homeOK {
			continue
		}
		d := Detection{Agent: a, BinaryPath: bin, SkillPath: resolve(a.Skill, homeDir)}
		_, d.Configured = cfg.Providers[a.ID]
		d.Status = status(d.SkillPath)
		out = append(out, d)
	}
	return out
}

func status(path string) Status {
	if path == "" {
		return StatusNew
	}
	b, err := os.ReadFile(path)
	if err != nil || !agentskill.HasMarker(b) {
		return StatusNew
	}
	if agentskill.InstalledVersion(b) < agentskill.Version {
		return StatusOutdated
	}
	return StatusUpToDate
}

// Install writes the rendered skill to d.SkillPath, creating directories.
func Install(d Detection) error {
	if err := os.MkdirAll(filepath.Dir(d.SkillPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(d.SkillPath, []byte(agentskill.SkillFile()), 0o644)
}
