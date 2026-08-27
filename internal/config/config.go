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
	Command      string `yaml:"command"`
	DefaultModel string `yaml:"default_model"`
}

type Preset struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Prompt   string `yaml:"prompt"`
	Args     string `yaml:"args"`
	Name     string `yaml:"name"`
}

type Global struct {
	Port           int                 `yaml:"port"`
	DataDir        string              `yaml:"data_dir"`
	Terminal       string              `yaml:"terminal"`
	SessionPattern string              `yaml:"session_pattern"`
	WtBin          string              `yaml:"wt_bin"`
	Providers      map[string]Provider `yaml:"providers"`
	Presets        map[string]Preset   `yaml:"presets"`
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
		WtBin:          "wt",
		Providers:      map[string]Provider{},
		Presets:        map[string]Preset{},
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
	return g, nil
}

// EncodeRepoPath maps an absolute repo path to its per-repo config dir
// name: every "/" becomes "-", keeping the leading dash (the same scheme
// Claude uses for project directories).
func EncodeRepoPath(repoRoot string) string {
	return strings.ReplaceAll(repoRoot, "/", "-")
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
