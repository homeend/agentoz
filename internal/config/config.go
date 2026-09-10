// Package config loads erbrus settings. Merge order: built-in defaults
// <- global config.yaml <- per-repo .erbrus.yaml. Settings never live in
// the database.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// winToPosix converts a Windows drive path (T:\a\b or T:/a/b) to its WSL
// form (/mnt/t/a/b). ok is false when p is not a Windows drive path.
func winToPosix(p string) (string, bool) {
	if len(p) < 3 {
		return "", false
	}
	drive := p[0]
	if !((drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')) {
		return "", false
	}
	if p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return "", false
	}
	rest := strings.ReplaceAll(p[2:], `\`, "/")
	return filepath.Clean("/mnt/" + strings.ToLower(string(drive)) + rest), true
}

// TranslateUserPath maps a user-supplied Windows drive path to the
// environment's native form (WSL /mnt/<drive>/...) when that translated
// directory actually exists; any other input is returned unchanged.
func TranslateUserPath(p string) string {
	cand, ok := winToPosix(p)
	if !ok {
		return p
	}
	if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
		return cand
	}
	return p
}

type Provider struct {
	Command string `yaml:"command"`
	// Type is what the provider is: "agent" (default, a coding agent that
	// takes a prompt and reports) or "tool" (a terminal program for the
	// human — no preamble, no prompt, no screen classification).
	Type         string `yaml:"type"`
	DefaultModel string `yaml:"default_model"`
	// Prompt is how the prompt reaches the CLI: "arg" (through {prompt}
	// in Command, the default when the template has it) or "paste" (typed
	// into the terminal once the input box is up — for CLIs whose
	// interactive mode takes no initial prompt).
	Prompt string `yaml:"prompt"`
	// Screen* are RE2 patterns classifying the agent's terminal (see
	// internal/screen). Any non-empty list replaces ALL built-in rules
	// for this provider; all empty = built-ins for this provider name.
	ScreenWorking  []string `yaml:"screen_working"`
	ScreenWaiting  []string `yaml:"screen_waiting"`
	ScreenQuestion []string `yaml:"screen_question"`
}

// ProviderTypes are the accepted values of Provider.Type ("" means agent).
var ProviderTypes = []string{"agent", "tool"}

// IsTool reports whether the provider is a human-facing terminal program
// rather than a coding agent.
func (p Provider) IsTool() bool { return p.Type == "tool" }

type Preset struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Prompt   string `yaml:"prompt"`
	Args     string `yaml:"args"`
	Name     string `yaml:"name"`
}

// Tool is a program the human opens in a channel's directory from the
// channel page: a terminal program (shell, gg — needs a TTY, runs in
// tmux and opens in the web terminal) or a GUI program (an editor,
// launched detached). Command is shell text with {dir}, {windir},
// {gg_bin} placeholders (see internal/tools).
type Tool struct {
	Command  string `yaml:"command"`
	Terminal bool   `yaml:"terminal"`
}

// BuiltinTools are merged into every config; a user entry of the same
// name wins.
func BuiltinTools() map[string]Tool {
	return map[string]Tool{
		"shell": {Command: "${SHELL:-bash}", Terminal: true},
		"gg":    {Command: "{gg_bin}", Terminal: true},
	}
}

type Global struct {
	Port           int    `yaml:"port"`
	DataDir        string `yaml:"data_dir"`
	Terminal       string `yaml:"terminal"`
	SessionPattern string `yaml:"session_pattern"`
	// Session is the shared "global" tmux session agents can be spawned
	// into instead of the per-project one.
	Session string `yaml:"session"`
	WtBin   string `yaml:"wt_bin"`
	// GgBin is the gigagit binary erbrus runs for worktree creation.
	GgBin     string              `yaml:"gg_bin"`
	Providers map[string]Provider `yaml:"providers"`
	Presets   map[string]Preset   `yaml:"presets"`
	Tools     map[string]Tool     `yaml:"tools"`
}

type Repo struct {
	Session            string            `yaml:"session"`
	AttachSession      string            `yaml:"attach_session"`
	ChannelPerWorktree *bool             `yaml:"channel_per_worktree"`
	Presets            map[string]Preset `yaml:"presets"`
}

func Defaults() Global {
	return Global{
		Port:           7420,
		Terminal:       "tmux",
		SessionPattern: "erbrus-{project}",
		Session:        "erbrus",
		WtBin:          "wt",
		GgBin:          "gg",
		Providers:      map[string]Provider{},
		Presets:        map[string]Preset{},
		Tools:          BuiltinTools(),
	}
}

func GlobalPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "erbrus", "config.yaml")
}

// LoadGlobal reads path over Defaults(). A missing file is not an error.
func LoadGlobal(path string) (Global, error) {
	g := Defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	if err := yaml.Unmarshal(data, &g); err != nil {
		return g, fmt.Errorf("parse %s: %w", path, err)
	}
	if g.Providers == nil {
		g.Providers = map[string]Provider{}
	}
	if g.Presets == nil {
		g.Presets = map[string]Preset{}
	}
	// Built-in tools survive a file that lists its own; a user entry of
	// the same name wins.
	if g.Tools == nil {
		g.Tools = map[string]Tool{}
	}
	for name, t := range BuiltinTools() {
		if _, ok := g.Tools[name]; !ok {
			g.Tools[name] = t
		}
	}
	if g.GgBin == "" {
		g.GgBin = "gg"
	}
	if g.Session == "" {
		g.Session = "erbrus"
	}
	return g, nil
}

// EncodeRepoPath maps an absolute repo path to its per-repo config dir
// name: every "/" becomes "-", keeping the leading dash (the same scheme
// Claude uses for project directories). Windows paths lose their "\" the
// same way and the drive colon, so T:\others\x is -T-others-x: a colon in
// a directory NAME is invalid there ("The filename, directory name, or
// volume label syntax is incorrect").
func EncodeRepoPath(repoRoot string) string {
	s := strings.NewReplacer("/", "-", `\`, "-", ":", "").Replace(repoRoot)
	if !strings.HasPrefix(s, "-") {
		s = "-" + s
	}
	return s
}

// erbrusHome is ~/.erbrus, overridable via ERBRUS_HOME (tests).
func erbrusHome() string {
	if h := os.Getenv("ERBRUS_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".erbrus")
}

// RepoConfigPath is where a repo's erbrus settings live — OUTSIDE the repo,
// so working trees stay clean.
func RepoConfigPath(repoRoot string) string {
	return filepath.Join(erbrusHome(), "repos", EncodeRepoPath(repoRoot), ".erbrus.yaml")
}

// LoadRepo reads the repo's config from RepoConfigPath, falling back to the
// legacy in-repo .erbrus.yaml (legacy=true) so callers can nudge migration.
func LoadRepo(repoRoot string) (Repo, bool, error) {
	var r Repo
	for _, cand := range []struct {
		path   string
		legacy bool
	}{
		{RepoConfigPath(repoRoot), false},
		{filepath.Join(repoRoot, ".erbrus.yaml"), true},
	} {
		data, err := os.ReadFile(cand.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return r, cand.legacy, err
		}
		if err := yaml.Unmarshal(data, &r); err != nil {
			return r, cand.legacy, fmt.Errorf("parse %s: %w", cand.path, err)
		}
		return r, cand.legacy, nil
	}
	return r, false, nil
}

func (g Global) ResolvedDataDir() string {
	if g.DataDir != "" {
		return ExpandHome(g.DataDir)
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "erbrus")
}

func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
