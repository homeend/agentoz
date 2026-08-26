# erbrus Plan 1/3 — Core Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working `erbrus` binary where `erbrus serve` runs the localhost server and `erbrus init` / `erbrus msg send` / `erbrus msg read` work end-to-end against it — projects, channels, messages, artifacts, SSE.

**Architecture:** One Go module `erbrus`, one binary. Layered YAML config → SQLite store → chi HTTP API + SSE hub → thin CLI client over the API. Spawning (tmux) is Plan 2; web UI is Plan 3.

**Tech Stack:** Go 1.26, chi v5, modernc.org/sqlite (pure Go), gopkg.in/yaml.v3. No other deps.

**Spec:** /mnt/t/others/erbrus/docs/superpowers/specs/2026-08-26-erbrus-design.md

## Global Constraints

Standing user rules — these override any default habit:

1. Always develop on a git worktree — never directly on main's checkout. **Execution starts by creating a worktree** (superpowers:using-git-worktrees; the repo's `wt` tool at /mnt/t/others/worktrees/bin/wt.bin also works: `wt new plan1-core`). All paths below are relative to that worktree root.
2. After finishing this plan, build and provide the binary's **absolute path** for the user's manual testing.
3. Do not merge to main; the user merges.
4. Never ask about pushing to remote, and never push.
5. Always reference files with absolute paths when talking to the user.

Spec-wide requirements:

- Server binds `127.0.0.1` only. No auth for human CLI/UI; bearer run-tokens for agents (tokens created in Plan 2; middleware lands here).
- Config: `~/.config/erbrus/config.yaml` (global) + `<repo>/.erbrus.yaml`; merge order defaults ← global ← per-repo. Honor `XDG_CONFIG_HOME` / `XDG_DATA_HOME`.
- State: `<data_dir>/erbrus.db`; artifacts: `<data_dir>/artifacts/<message-id>/<filename>`; default data dir `~/.local/share/erbrus`.
- Settings never live in the DB.
- Pinned decisions (spec's open items): router = **chi v5**; CLI = **stdlib `flag` with manual subcommand dispatch** (no cobra); message bodies are stored raw and (in Plan 3) rendered as escaped plain text — no markdown library.
- Module name: `erbrus`. Default port: `7420`. Session pattern default: `erbrus-{project}` (used in Plan 2 but part of config now).
- TDD throughout: write the failing test first, watch it fail, implement, watch it pass, commit.

---

### Task 1: Module scaffold and subcommand dispatch

**Files:**
- Create: `go.mod`, `cmd/erbrus/main.go`, `internal/cli/cli.go`
- Test: `internal/cli/cli_test.go`
- Create: `.gitignore`

**Interfaces:**
- Consumes: nothing.
- Produces: `cli.Run(args []string, stdout, stderr io.Writer) int` — every later CLI task registers a subcommand inside `cli.Run`'s switch. `main.go` stays a 5-line shim forever.

- [ ] **Step 1: Initialize module and scaffold files**

```bash
go mod init erbrus
```

`.gitignore`:

```
/bin/
*.db
```

`cmd/erbrus/main.go`:

```go
package main

import (
	"os"

	"erbrus/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

- [ ] **Step 2: Write the failing test**

`internal/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"version"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "erbrus ") {
		t.Fatalf("output %q, want prefix %q", out.String(), "erbrus ")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"bogus"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr %q, want to contain 'unknown command'", errOut.String())
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(nil, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage:") {
		t.Fatalf("stderr %q, want usage text", errOut.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cli/ -v`
Expected: FAIL — `undefined: Run`

- [ ] **Step 4: Write minimal implementation**

`internal/cli/cli.go`:

```go
// Package cli dispatches erbrus subcommands. main() is a shim over Run.
package cli

import (
	"fmt"
	"io"
)

const Version = "0.1.0-dev"

const usage = `usage: erbrus <command> [flags]

commands:
  serve      run the server
  init       register the current repo as a project (auto-configure)
  msg        send/read channel messages (msg send | msg read)
  version    print version
`

// Run executes one subcommand and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "erbrus %s\n", Version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ -v && go build ./...`
Expected: PASS, clean build

- [ ] **Step 6: Commit**

```bash
git add go.mod .gitignore cmd/erbrus/main.go internal/cli/
git commit -m "feat: scaffold erbrus module with subcommand dispatch"
```

---

### Task 2: Layered configuration (internal/config)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
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
func Defaults() Global
func GlobalPath() string                       // $XDG_CONFIG_HOME or ~/.config, + /erbrus/config.yaml
func LoadGlobal(path string) (Global, error)   // missing file => Defaults(), no error
func LoadRepo(repoRoot string) (Repo, error)   // reads <repoRoot>/.erbrus.yaml; missing => zero Repo, no error
func (g Global) ResolvedDataDir() string       // g.DataDir or $XDG_DATA_HOME or ~/.local/share, + /erbrus; tilde expanded
func ExpandHome(p string) string
```

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go`:

```go
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
	root := t.TempDir()
	write(t, filepath.Join(root, ".erbrus.yaml"),
		"session: my-sess\nchannel_per_worktree: false\npresets:\n  claude:\n    provider: claude-code\n")
	r, err := LoadRepo(root)
	if err != nil {
		t.Fatal(err)
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
	r, err := LoadRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go get gopkg.in/yaml.v3 && go test ./internal/config/ -v`
Expected: FAIL — undefined symbols (the `go get` first, so the failure is the right one)

- [ ] **Step 3: Implement**

`internal/config/config.go`:

```go
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

// LoadRepo reads <repoRoot>/.erbrus.yaml. A missing file yields a zero Repo.
func LoadRepo(repoRoot string) (Repo, error) {
	var r Repo
	path := filepath.Join(repoRoot, ".erbrus.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := yaml.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("parse %s: %w", path, err)
	}
	return r, nil
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
```

Then: `go get gopkg.in/yaml.v3`

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (all 9 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat: layered YAML config with XDG paths"
```

---

### Task 3: Provider command templates (internal/provider)

**Files:**
- Create: `internal/provider/provider.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: `config.Provider`.
- Produces:

```go
type Registry map[string]config.Provider
// Render builds the full shell command line for a provider.
// Placeholders {model} {args} {prompt}. Empty model falls back to
// DefaultModel; a still-empty placeholder drops its whole token, and if the
// preceding token is a flag (starts with "-"), that flag is dropped too.
// The prompt value is single-quote shell-escaped in place of "{prompt}".
func (r Registry) Render(name, model, args, prompt string) (string, error)
func ShellQuote(s string) string
```

- [ ] **Step 1: Write the failing tests**

`internal/provider/provider_test.go`:

```go
package provider

import (
	"strings"
	"testing"

	"erbrus/internal/config"
)

func reg() Registry {
	return Registry{
		"claude-code": {Command: `claude --model {model} {args} "{prompt}"`, DefaultModel: "fable-5"},
		"codex":       {Command: `codex {args} "{prompt}"`},
		"kimi":        {Command: `kimi --model {model} {args} "{prompt}"`},
	}
}

func TestRenderAllSet(t *testing.T) {
	got, err := reg().Render("kimi", "k3", "--max-turns 30", "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	want := `kimi --model k3 --max-turns 30 'do the thing'`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderDefaultModel(t *testing.T) {
	got, err := reg().Render("claude-code", "", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "--model fable-5") {
		t.Errorf("default model not applied: %q", got)
	}
}

func TestRenderEmptyModelDropsFlag(t *testing.T) {
	got, err := reg().Render("kimi", "", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	want := `kimi 'hi'`
	if got != want {
		t.Errorf("got %q, want %q (flag+placeholder dropped)", got, want)
	}
}

func TestRenderEmptyArgsDropsToken(t *testing.T) {
	got, err := reg().Render("codex", "", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got != `codex 'hi'` {
		t.Errorf("got %q", got)
	}
}

func TestRenderPromptEscaping(t *testing.T) {
	got, err := reg().Render("codex", "", "", `it's "quoted" $HOME`)
	if err != nil {
		t.Fatal(err)
	}
	want := `codex 'it'\''s "quoted" $HOME'`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderUnknownProvider(t *testing.T) {
	if _, err := reg().Render("nope", "", "", "x"); err == nil {
		t.Fatal("want error for unknown provider")
	}
}

func TestShellQuote(t *testing.T) {
	if got := ShellQuote(`a'b`); got != `'a'\''b'` {
		t.Errorf("ShellQuote = %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/ -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement**

`internal/provider/provider.go`:

```go
// Package provider renders provider CLI command lines from config
// templates. Adding a provider is configuration, not code.
package provider

import (
	"fmt"
	"strings"

	"erbrus/internal/config"
)

type Registry map[string]config.Provider

// Render substitutes {model}, {args}, {prompt} into the provider's command
// template. Tokens whose placeholder resolves empty are dropped, along with
// an immediately preceding flag token. The prompt is shell-quoted; the
// template's own quotes around {prompt} are replaced by ours.
func (r Registry) Render(name, model, args, prompt string) (string, error) {
	p, ok := r[name]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", name)
	}
	if model == "" {
		model = p.DefaultModel
	}
	vals := map[string]string{"model": model, "args": args, "prompt": ShellQuote(prompt)}

	tokens := strings.Fields(p.Command)
	var out []string
	for _, tok := range tokens {
		key, isPlaceholder := placeholderKey(tok)
		if !isPlaceholder {
			out = append(out, tok)
			continue
		}
		v := vals[key]
		if v == "" {
			// Drop the placeholder; drop a preceding flag too.
			if len(out) > 0 && strings.HasPrefix(out[len(out)-1], "-") {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, v)
	}
	return strings.Join(out, " "), nil
}

// placeholderKey recognizes {x}, "{x}", '{x}' as placeholder tokens.
func placeholderKey(tok string) (string, bool) {
	t := strings.Trim(tok, `"'`)
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// ShellQuote single-quotes s for POSIX shells.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -v`
Expected: PASS. Note the multi-word `args` value ("--max-turns 30") lands as-is mid-command — verify TestRenderAllSet's exact output.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/
git commit -m "feat: provider command template renderer"
```

---

### Task 4: Preset merge (internal/preset)

**Files:**
- Create: `internal/preset/preset.go`
- Test: `internal/preset/preset_test.go`

**Interfaces:**
- Consumes: `config.Preset`.
- Produces:

```go
// Merge overlays project presets on global ones; same name replaces whole
// preset. Inputs are not mutated.
func Merge(global, project map[string]config.Preset) map[string]config.Preset
// Resolve returns the named preset or an error listing available names.
func Resolve(name string, merged map[string]config.Preset) (config.Preset, error)
```

- [ ] **Step 1: Write the failing tests**

`internal/preset/preset_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/preset/ -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement**

`internal/preset/preset.go`:

```go
// Package preset merges agent presets: global config presets overlaid by a
// project's .erbrus.yaml presets. A preset only prefills a spawn request;
// every field stays overridable at spawn time.
package preset

import (
	"fmt"
	"sort"
	"strings"

	"erbrus/internal/config"
)

func Merge(global, project map[string]config.Preset) map[string]config.Preset {
	m := make(map[string]config.Preset, len(global)+len(project))
	for k, v := range global {
		m[k] = v
	}
	for k, v := range project {
		m[k] = v
	}
	return m
}

func Resolve(name string, merged map[string]config.Preset) (config.Preset, error) {
	if p, ok := merged[name]; ok {
		return p, nil
	}
	names := make([]string, 0, len(merged))
	for k := range merged {
		names = append(names, k)
	}
	sort.Strings(names)
	return config.Preset{}, fmt.Errorf("unknown preset %q (available: %s)", name, strings.Join(names, ", "))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/preset/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/preset/
git commit -m "feat: preset merge and resolution"
```

---

### Task 5: Worktree detection (internal/wt)

**Files:**
- Create: `internal/wt/wt.go`
- Test: `internal/wt/wt_test.go`

**Interfaces:**
- Consumes: nothing (exec via injectable runner).
- Produces:

```go
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`   // e.g. "refs/heads/main"
	Head   string `json:"head"`
	IsMain bool   `json:"is_main"`
}
// Runner executes a command and returns stdout. Production: exec.Command.
type Runner func(dir, name string, args ...string) ([]byte, error)
func ExecRunner(dir, name string, args ...string) ([]byte, error)
// List detects worktrees for repoRoot: tries `<wtBin> list --json -r root`,
// falls back to `git worktree list --porcelain`. Error only if both fail.
func List(run Runner, wtBin, repoRoot string) ([]Worktree, error)
// GitRoot returns the toplevel of the worktree containing dir.
func GitRoot(run Runner, dir string) (string, error)
// MainRoot returns the main worktree's root for the repo containing dir.
func MainRoot(run Runner, dir string) (string, error)
func ParsePorcelain(out []byte) []Worktree
// ShortBranch turns "refs/heads/wt/x" into "wt/x".
func ShortBranch(ref string) string
```

- [ ] **Step 1: Write the failing tests**

`internal/wt/wt_test.go`:

```go
package wt

import (
	"errors"
	"fmt"
	"testing"
)

// fake returns a Runner serving canned outputs keyed by "name args...".
func fake(outputs map[string]string) Runner {
	return func(dir, name string, args ...string) ([]byte, error) {
		key := name
		for _, a := range args {
			key += " " + a
		}
		if out, ok := outputs[key]; ok {
			return []byte(out), nil
		}
		return nil, fmt.Errorf("no fake for %q", key)
	}
}

const wtJSON = `[
  {"path": "/repo", "branch": "refs/heads/main", "head": "abc", "is_main": true},
  {"path": "/repo-wt/feat", "branch": "refs/heads/wt/feat", "head": "def", "is_main": false}
]`

func TestListViaWt(t *testing.T) {
	run := fake(map[string]string{"wt list --json -r /repo": wtJSON})
	wts, err := List(run, "wt", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("len = %d, want 2", len(wts))
	}
	if !wts[0].IsMain || wts[0].Path != "/repo" {
		t.Errorf("main worktree wrong: %+v", wts[0])
	}
	if wts[1].Branch != "refs/heads/wt/feat" {
		t.Errorf("branch = %q", wts[1].Branch)
	}
}

const porcelain = `worktree /repo
HEAD abcabcabc
branch refs/heads/main

worktree /repo-wt/feat
HEAD defdefdef
branch refs/heads/wt/feat

`

func TestListFallsBackToGit(t *testing.T) {
	run := func(dir, name string, args ...string) ([]byte, error) {
		if name == "wt" {
			return nil, errors.New("wt: not found")
		}
		return []byte(porcelain), nil
	}
	wts, err := List(run, "wt", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("len = %d, want 2", len(wts))
	}
	if !wts[0].IsMain {
		t.Error("first porcelain entry is the main worktree")
	}
	if wts[1].IsMain {
		t.Error("second entry must not be main")
	}
}

func TestListBothFail(t *testing.T) {
	run := func(dir, name string, args ...string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	if _, err := List(run, "wt", "/repo"); err == nil {
		t.Fatal("want error when wt and git both fail")
	}
}

func TestParsePorcelainDetachedHead(t *testing.T) {
	out := []byte("worktree /repo\nHEAD abc\ndetached\n\n")
	wts := ParsePorcelain(out)
	if len(wts) != 1 || wts[0].Branch != "" {
		t.Errorf("detached entry parsed wrong: %+v", wts)
	}
}

func TestGitRoot(t *testing.T) {
	run := fake(map[string]string{"git rev-parse --show-toplevel": "/repo-wt/feat\n"})
	root, err := GitRoot(run, "/repo-wt/feat/sub")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/repo-wt/feat" {
		t.Errorf("root = %q", root)
	}
}

func TestMainRoot(t *testing.T) {
	run := fake(map[string]string{"git worktree list --porcelain": porcelain})
	root, err := MainRoot(run, "/repo-wt/feat")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/repo" {
		t.Errorf("main root = %q", root)
	}
}

func TestShortBranch(t *testing.T) {
	if got := ShortBranch("refs/heads/wt/feat"); got != "wt/feat" {
		t.Errorf("ShortBranch = %q", got)
	}
	if got := ShortBranch("main"); got != "main" {
		t.Errorf("ShortBranch = %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/wt/ -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement**

`internal/wt/wt.go`:

```go
// Package wt detects git worktrees, preferring the user's `wt` tool
// (`wt list --json -r <root>`) and falling back to
// `git worktree list --porcelain`.
package wt

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	IsMain bool   `json:"is_main"`
}

type Runner func(dir, name string, args ...string) ([]byte, error)

func ExecRunner(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

func List(run Runner, wtBin, repoRoot string) ([]Worktree, error) {
	if out, err := run(repoRoot, wtBin, "list", "--json", "-r", repoRoot); err == nil {
		var wts []Worktree
		if jerr := json.Unmarshal(out, &wts); jerr == nil && len(wts) > 0 {
			return wts, nil
		}
	}
	out, err := run(repoRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("worktree detection failed (wt and git): %w", err)
	}
	return ParsePorcelain(out), nil
}

// ParsePorcelain parses `git worktree list --porcelain`. The first entry is
// the main worktree.
func ParsePorcelain(out []byte) []Worktree {
	var wts []Worktree
	var cur *Worktree
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if cur != nil {
				wts = append(wts, *cur)
			}
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			if cur != nil {
				cur.Head = strings.TrimPrefix(line, "HEAD ")
			}
		case strings.HasPrefix(line, "branch "):
			if cur != nil {
				cur.Branch = strings.TrimPrefix(line, "branch ")
			}
		}
	}
	if cur != nil {
		wts = append(wts, *cur)
	}
	if len(wts) > 0 {
		wts[0].IsMain = true
	}
	return wts
}

func GitRoot(run Runner, dir string) (string, error) {
	out, err := run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// MainRoot finds the main worktree's path for the repo containing dir.
func MainRoot(run Runner, dir string) (string, error) {
	out, err := run(dir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	wts := ParsePorcelain(out)
	if len(wts) == 0 {
		return "", fmt.Errorf("no worktrees found from %s", dir)
	}
	return wts[0].Path, nil
}

func ShortBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/wt/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/wt/
git commit -m "feat: worktree detection via wt with git porcelain fallback"
```

---

### Task 6: SQLite store (internal/store)

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.sql`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (types used by server, client, and Plan 2):

```go
type Store struct{ /* private db */ }
func Open(path string) (*Store, error)   // creates file, runs schema
func (s *Store) Close() error

type Project struct{ ID int64; Name, RepoPath string; CreatedAt time.Time }
type Channel struct{ ID, ProjectID int64; Name, WorktreePath, Branch string; Archived bool; CreatedAt time.Time }
type Message struct{ ID, ChannelID int64; Kind, AuthorKind, AuthorName, Body string; AgentRunID, OriginMessageID int64; CreatedAt time.Time } // 0 = null for the int64 refs
type Artifact struct{ ID, MessageID int64; Filename, Path string; Size int64; CreatedAt time.Time }
type AgentRun struct{ ID, ChannelID int64; PresetName, Provider, AgentName, Model, ExtraArgs, Prompt, Workdir, Status, Spawner, TmuxTarget, Token string; ExitCode int64; HasExit bool; OriginMessageID int64; CreatedAt, FinishedAt time.Time }

CreateProject(name, repoPath string) (Project, error)
ProjectByPath(repoPath string) (Project, bool, error)
ProjectByID(id int64) (Project, bool, error)
Projects() ([]Project, error)
CreateChannel(projectID int64, name, worktreePath, branch string) (Channel, error)
ChannelByID(id int64) (Channel, bool, error)
ChannelByName(projectID int64, name string) (Channel, bool, error)
ChannelsByProject(projectID int64) ([]Channel, error)
CreateMessage(m Message) (Message, error)          // fills ID, CreatedAt
MessageByID(id int64) (Message, bool, error)
MessagesSince(channelID, sinceID int64, limit int) ([]Message, error)
AddArtifact(a Artifact) (Artifact, error)
ArtifactsByMessage(messageID int64) ([]Artifact, error)
CreateRun(r AgentRun) (AgentRun, error)            // fills ID, Token, CreatedAt
RunByToken(token string) (AgentRun, bool, error)
RunByID(id int64) (AgentRun, bool, error)
RunsByChannel(channelID int64) ([]AgentRun, error)
RunningRuns() ([]AgentRun, error)
FinishRun(id int64, status string, exitCode int64) error
```

Notes for the implementer: `kind` ∈ message|report|system; `author_kind` ∈ agent|human|system; `status` ∈ starting|running|done|failed|stopped; `spawner` ∈ tmux|fg. `AuthorName` denormalizes the display name so rendering never joins. Token = 32 hex chars from crypto/rand.

- [ ] **Step 1: Write the schema**

`internal/store/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS projects (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  repo_path  TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS channels (
  id            INTEGER PRIMARY KEY,
  project_id    INTEGER NOT NULL REFERENCES projects(id),
  name          TEXT NOT NULL,
  worktree_path TEXT NOT NULL DEFAULT '',
  branch        TEXT NOT NULL DEFAULT '',
  archived      INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(project_id, name)
);
CREATE TABLE IF NOT EXISTS messages (
  id                INTEGER PRIMARY KEY,
  channel_id        INTEGER NOT NULL REFERENCES channels(id),
  kind              TEXT NOT NULL CHECK (kind IN ('message','report','system')),
  author_kind       TEXT NOT NULL CHECK (author_kind IN ('agent','human','system')),
  author_name       TEXT NOT NULL DEFAULT '',
  agent_run_id      INTEGER REFERENCES agent_runs(id),
  origin_message_id INTEGER REFERENCES messages(id),
  body              TEXT NOT NULL,
  created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, id);
CREATE TABLE IF NOT EXISTS artifacts (
  id         INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES messages(id),
  filename   TEXT NOT NULL,
  path       TEXT NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS agent_runs (
  id                INTEGER PRIMARY KEY,
  channel_id        INTEGER NOT NULL REFERENCES channels(id),
  preset_name       TEXT NOT NULL DEFAULT '',
  provider          TEXT NOT NULL,
  agent_name        TEXT NOT NULL,
  model             TEXT NOT NULL DEFAULT '',
  extra_args        TEXT NOT NULL DEFAULT '',
  prompt            TEXT NOT NULL DEFAULT '',
  workdir           TEXT NOT NULL,
  status            TEXT NOT NULL CHECK (status IN ('starting','running','done','failed','stopped')),
  exit_code         INTEGER,
  spawner           TEXT NOT NULL CHECK (spawner IN ('tmux','fg')),
  tmux_target       TEXT NOT NULL DEFAULT '',
  token             TEXT NOT NULL UNIQUE,
  origin_message_id INTEGER REFERENCES messages(id),
  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
  finished_at       TEXT
);
```

- [ ] **Step 2: Write the failing tests**

`internal/store/store_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProjectRoundtrip(t *testing.T) {
	s := open(t)
	p, err := s.CreateProject("webshop", "/code/webshop")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Fatal("ID not assigned")
	}
	got, ok, err := s.ProjectByPath("/code/webshop")
	if err != nil || !ok || got.Name != "webshop" {
		t.Fatalf("ProjectByPath: %v %v %+v", err, ok, got)
	}
	_, ok, err = s.ProjectByPath("/nope")
	if err != nil || ok {
		t.Fatal("missing project should be ok=false, no error")
	}
	if _, err := s.CreateProject("webshop2", "/code/webshop"); err == nil {
		t.Fatal("duplicate repo_path must error")
	}
	all, err := s.Projects()
	if err != nil || len(all) != 1 {
		t.Fatalf("Projects: %v len=%d", err, len(all))
	}
}

func TestChannelRoundtrip(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, err := s.CreateChannel(p.ID, "general", "/x", "main")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.ChannelByName(p.ID, "general")
	if !ok || got.ID != c.ID || got.Branch != "main" {
		t.Fatalf("ChannelByName: %+v", got)
	}
	list, _ := s.ChannelsByProject(p.ID)
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	if _, err := s.CreateChannel(p.ID, "general", "", ""); err == nil {
		t.Fatal("duplicate channel name in project must error")
	}
}

func TestMessagesSinceAndArtifacts(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, _ := s.CreateChannel(p.ID, "general", "", "")
	var last Message
	for _, body := range []string{"one", "two", "three"} {
		m, err := s.CreateMessage(Message{ChannelID: c.ID, Kind: "message", AuthorKind: "human", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		last = m
	}
	a, err := s.AddArtifact(Artifact{MessageID: last.ID, Filename: "r.md", Path: "/data/r.md", Size: 5})
	if err != nil || a.ID == 0 {
		t.Fatalf("AddArtifact: %v", err)
	}
	msgs, err := s.MessagesSince(c.ID, 0, 100)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("MessagesSince all: %v len=%d", err, len(msgs))
	}
	if msgs[0].Body != "one" {
		t.Error("messages must come back oldest-first")
	}
	msgs, _ = s.MessagesSince(c.ID, msgs[0].ID, 100)
	if len(msgs) != 2 || msgs[0].Body != "two" {
		t.Fatalf("since filter wrong: %+v", msgs)
	}
	msgs, _ = s.MessagesSince(c.ID, 0, 2)
	if len(msgs) != 2 {
		t.Fatalf("limit ignored: len=%d", len(msgs))
	}
	arts, _ := s.ArtifactsByMessage(last.ID)
	if len(arts) != 1 || arts[0].Filename != "r.md" {
		t.Fatalf("ArtifactsByMessage: %+v", arts)
	}
}

func TestRunLifecycle(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, _ := s.CreateChannel(p.ID, "general", "", "")
	r, err := s.CreateRun(AgentRun{
		ChannelID: c.ID, Provider: "codex", AgentName: "impl",
		Workdir: "/x", Status: "starting", Spawner: "tmux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Token) != 32 {
		t.Fatalf("token %q, want 32 hex chars", r.Token)
	}
	got, ok, _ := s.RunByToken(r.Token)
	if !ok || got.ID != r.ID {
		t.Fatal("RunByToken failed")
	}
	running, _ := s.RunningRuns()
	if len(running) != 1 {
		t.Fatalf("RunningRuns len = %d (starting counts as running)", len(running))
	}
	if err := s.FinishRun(r.ID, "done", 0); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.RunByID(r.ID)
	if got.Status != "done" || !got.HasExit || got.ExitCode != 0 || got.FinishedAt.IsZero() {
		t.Fatalf("after FinishRun: %+v", got)
	}
	running, _ = s.RunningRuns()
	if len(running) != 0 {
		t.Fatal("finished run still listed as running")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go get modernc.org/sqlite && go test ./internal/store/ -v`
Expected: FAIL — undefined symbols (the `go get` first, so the failure is the right one)

- [ ] **Step 4: Implement**

`internal/store/store.go` (schema embedded via `go:embed`):

```go
// Package store is erbrus's SQLite persistence: runtime state and history
// only — settings live in YAML files, never here.
package store

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// modernc sqlite: single writer; avoid SQLITE_BUSY from pooling.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Project struct {
	ID        int64
	Name      string
	RepoPath  string
	CreatedAt time.Time
}

type Channel struct {
	ID           int64
	ProjectID    int64
	Name         string
	WorktreePath string
	Branch       string
	Archived     bool
	CreatedAt    time.Time
}

type Message struct {
	ID              int64
	ChannelID       int64
	Kind            string
	AuthorKind      string
	AuthorName      string
	AgentRunID      int64 // 0 = none
	OriginMessageID int64 // 0 = none
	Body            string
	CreatedAt       time.Time
}

type Artifact struct {
	ID        int64
	MessageID int64
	Filename  string
	Path      string
	Size      int64
	CreatedAt time.Time
}

type AgentRun struct {
	ID              int64
	ChannelID       int64
	PresetName      string
	Provider        string
	AgentName       string
	Model           string
	ExtraArgs       string
	Prompt          string
	Workdir         string
	Status          string
	ExitCode        int64
	HasExit         bool
	Spawner         string
	TmuxTarget      string
	Token           string
	OriginMessageID int64
	CreatedAt       time.Time
	FinishedAt      time.Time
}

const timeFmt = "2006-01-02 15:04:05"

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeFmt, s)
	return t
}

// nz maps 0 -> NULL for optional foreign keys.
func nz(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *Store) CreateProject(name, repoPath string) (Project, error) {
	res, err := s.db.Exec(`INSERT INTO projects (name, repo_path) VALUES (?, ?)`, name, repoPath)
	if err != nil {
		return Project{}, err
	}
	id, _ := res.LastInsertId()
	p, _, err := s.ProjectByID(id)
	return p, err
}

func (s *Store) scanProject(row *sql.Row) (Project, bool, error) {
	var p Project
	var ts string
	err := row.Scan(&p.ID, &p.Name, &p.RepoPath, &ts)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.CreatedAt = parseTime(ts)
	return p, true, nil
}

func (s *Store) ProjectByID(id int64) (Project, bool, error) {
	return s.scanProject(s.db.QueryRow(`SELECT id, name, repo_path, created_at FROM projects WHERE id = ?`, id))
}

func (s *Store) ProjectByPath(repoPath string) (Project, bool, error) {
	return s.scanProject(s.db.QueryRow(`SELECT id, name, repo_path, created_at FROM projects WHERE repo_path = ?`, repoPath))
}

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, repo_path, created_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var ts string
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoPath, &ts); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(ts)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateChannel(projectID int64, name, worktreePath, branch string) (Channel, error) {
	res, err := s.db.Exec(`INSERT INTO channels (project_id, name, worktree_path, branch) VALUES (?, ?, ?, ?)`,
		projectID, name, worktreePath, branch)
	if err != nil {
		return Channel{}, err
	}
	id, _ := res.LastInsertId()
	c, _, err := s.ChannelByID(id)
	return c, err
}

const chanCols = `id, project_id, name, worktree_path, branch, archived, created_at`

func scanChannel(sc interface{ Scan(...any) error }) (Channel, error) {
	var c Channel
	var ts string
	err := sc.Scan(&c.ID, &c.ProjectID, &c.Name, &c.WorktreePath, &c.Branch, &c.Archived, &ts)
	c.CreatedAt = parseTime(ts)
	return c, err
}

func (s *Store) ChannelByID(id int64) (Channel, bool, error) {
	c, err := scanChannel(s.db.QueryRow(`SELECT `+chanCols+` FROM channels WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *Store) ChannelByName(projectID int64, name string) (Channel, bool, error) {
	c, err := scanChannel(s.db.QueryRow(`SELECT `+chanCols+` FROM channels WHERE project_id = ? AND name = ?`, projectID, name))
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *Store) ChannelsByProject(projectID int64) ([]Channel, error) {
	rows, err := s.db.Query(`SELECT `+chanCols+` FROM channels WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateMessage(m Message) (Message, error) {
	res, err := s.db.Exec(
		`INSERT INTO messages (channel_id, kind, author_kind, author_name, agent_run_id, origin_message_id, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ChannelID, m.Kind, m.AuthorKind, m.AuthorName, nz(m.AgentRunID), nz(m.OriginMessageID), m.Body)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	got, _, err := s.MessageByID(id)
	return got, err
}

const msgCols = `id, channel_id, kind, author_kind, author_name,
	COALESCE(agent_run_id, 0), COALESCE(origin_message_id, 0), body, created_at`

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var ts string
	err := sc.Scan(&m.ID, &m.ChannelID, &m.Kind, &m.AuthorKind, &m.AuthorName,
		&m.AgentRunID, &m.OriginMessageID, &m.Body, &ts)
	m.CreatedAt = parseTime(ts)
	return m, err
}

func (s *Store) MessageByID(id int64) (Message, bool, error) {
	m, err := scanMessage(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return m, false, nil
	}
	return m, err == nil, err
}

func (s *Store) MessagesSince(channelID, sinceID int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages
		WHERE channel_id = ? AND id > ? ORDER BY id LIMIT ?`, channelID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddArtifact(a Artifact) (Artifact, error) {
	res, err := s.db.Exec(`INSERT INTO artifacts (message_id, filename, path, size) VALUES (?, ?, ?, ?)`,
		a.MessageID, a.Filename, a.Path, a.Size)
	if err != nil {
		return Artifact{}, err
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

func (s *Store) ArtifactsByMessage(messageID int64) ([]Artifact, error) {
	rows, err := s.db.Query(`SELECT id, message_id, filename, path, size, created_at
		FROM artifacts WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		var ts string
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.Path, &a.Size, &ts); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(ts)
		out = append(out, a)
	}
	return out, rows.Err()
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) CreateRun(r AgentRun) (AgentRun, error) {
	r.Token = newToken()
	res, err := s.db.Exec(
		`INSERT INTO agent_runs (channel_id, preset_name, provider, agent_name, model, extra_args,
			prompt, workdir, status, spawner, tmux_target, token, origin_message_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ChannelID, r.PresetName, r.Provider, r.AgentName, r.Model, r.ExtraArgs,
		r.Prompt, r.Workdir, r.Status, r.Spawner, r.TmuxTarget, r.Token, nz(r.OriginMessageID))
	if err != nil {
		return AgentRun{}, err
	}
	id, _ := res.LastInsertId()
	got, _, err := s.RunByID(id)
	return got, err
}

const runCols = `id, channel_id, preset_name, provider, agent_name, model, extra_args,
	prompt, workdir, status, exit_code, spawner, tmux_target, token,
	COALESCE(origin_message_id, 0), created_at, COALESCE(finished_at, '')`

func scanRun(sc interface{ Scan(...any) error }) (AgentRun, error) {
	var r AgentRun
	var exit sql.NullInt64
	var created, finished string
	err := sc.Scan(&r.ID, &r.ChannelID, &r.PresetName, &r.Provider, &r.AgentName, &r.Model,
		&r.ExtraArgs, &r.Prompt, &r.Workdir, &r.Status, &exit, &r.Spawner, &r.TmuxTarget,
		&r.Token, &r.OriginMessageID, &created, &finished)
	r.ExitCode, r.HasExit = exit.Int64, exit.Valid
	r.CreatedAt = parseTime(created)
	if finished != "" {
		r.FinishedAt = parseTime(finished)
	}
	return r, err
}

func (s *Store) RunByID(id int64) (AgentRun, bool, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM agent_runs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) RunByToken(token string) (AgentRun, bool, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM agent_runs WHERE token = ?`, token))
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) runsWhere(where string, args ...any) ([]AgentRun, error) {
	rows, err := s.db.Query(`SELECT `+runCols+` FROM agent_runs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RunsByChannel(channelID int64) ([]AgentRun, error) {
	return s.runsWhere(`WHERE channel_id = ? ORDER BY id`, channelID)
}

func (s *Store) RunningRuns() ([]AgentRun, error) {
	return s.runsWhere(`WHERE status IN ('starting','running') ORDER BY id`)
}

func (s *Store) FinishRun(id int64, status string, exitCode int64) error {
	_, err := s.db.Exec(`UPDATE agent_runs SET status = ?, exit_code = ?, finished_at = datetime('now') WHERE id = ?`,
		status, exitCode, id)
	return err
}
```

Then: `go get modernc.org/sqlite`

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS (4 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: SQLite store with schema and CRUD"
```

---

### Task 7: HTTP API — projects, channels, messages (internal/server)

**Files:**
- Create: `internal/server/server.go`, `internal/server/projects.go`, `internal/server/messages.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `store.*`, `config.Global`, `config.LoadRepo`, `preset.Merge`, `wt.List`/`wt.Runner`/`wt.ShortBranch`.
- Produces:

```go
type Server struct { /* private: store, cfg, hub, run wt.Runner, dataDir string */ }
func New(st *store.Store, cfg config.Global, run wt.Runner) *Server
func (s *Server) Handler() http.Handler   // chi router with all routes
// JSON envelopes (used by client task and Plan 3):
type projectJSON struct {
	ID       int64         `json:"id"`
	Name     string        `json:"name"`
	RepoPath string        `json:"repo_path"`
	Channels []channelJSON `json:"channels"`
}
type channelJSON struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}
type messageJSON struct {
	ID              int64          `json:"id"`
	ChannelID       int64          `json:"channel_id"`
	Kind            string         `json:"kind"`
	AuthorKind      string         `json:"author_kind"`
	AuthorName      string         `json:"author_name"`
	OriginMessageID int64          `json:"origin_message_id,omitempty"`
	Body            string         `json:"body"`
	CreatedAt       string         `json:"created_at"`
	Artifacts       []artifactJSON `json:"artifacts,omitempty"`
}
type artifactJSON struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}
```

Routes in this task:

```
POST /api/projects            {"repo_path": "..."}  -> 200 projectJSON (idempotent: existing project returned with created=false)
GET  /api/projects            -> 200 [projectJSON]
GET  /api/channels/{id}/messages?since=&limit=  -> 200 [messageJSON]
POST /api/channels/{id}/messages
     JSON {"kind","body"} or multipart (field "body", "kind", files "file")
     auth: Bearer <run token> => author agent/<run.AgentName>; none => human
     -> 201 messageJSON
```

Behavior of POST /api/projects (this **is** auto-configure, server-side): expand/absolutize repo_path → if project exists, return it → else detect worktrees (`wt.List`) → create project named `filepath.Base(root)` → create `general` channel for the main worktree → if the repo's `.erbrus.yaml` has `channel_per_worktree` true **or unset**, create one channel per non-main worktree named `wt.ShortBranch(branch)` (fallback: `filepath.Base(path)` when branch empty). Detection failure is not fatal: create project + `general` only, include `"warning"` in the response JSON.

- [ ] **Step 1: Write the failing tests**

`internal/server/server_test.go`:

```go
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/store"
)

const wtJSON = `[
  {"path": "%s", "branch": "refs/heads/main", "head": "abc", "is_main": true},
  {"path": "%s", "branch": "refs/heads/wt/feat", "head": "def", "is_main": false}
]`

// newTestServer returns the server, its store, and a fake repo root whose
// worktree listing contains main + one worktree.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()
	wtOut := fmt.Sprintf(wtJSON, root, root+"-wt-feat")
	run := func(dir, name string, args ...string) ([]byte, error) {
		if name == "wt" {
			return []byte(wtOut), nil
		}
		return nil, fmt.Errorf("unexpected exec %s", name)
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	srv := New(st, cfg, run)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, root
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAddProjectCreatesChannels(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	p := decode[map[string]any](t, resp)
	if p["name"] != filepath.Base(root) {
		t.Errorf("name = %v", p["name"])
	}
	chans := p["channels"].([]any)
	if len(chans) != 2 {
		t.Fatalf("channels = %d, want 2 (general + wt/feat)", len(chans))
	}
	names := []string{
		chans[0].(map[string]any)["name"].(string),
		chans[1].(map[string]any)["name"].(string),
	}
	if names[0] != "general" || names[1] != "wt/feat" {
		t.Errorf("channel names = %v", names)
	}
}

func TestAddProjectIdempotent(t *testing.T) {
	ts, _, root := newTestServer(t)
	postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root}).Body.Close()
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second add status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	listResp, _ := http.Get(ts.URL + "/api/projects")
	projects := decode[[]map[string]any](t, listResp)
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}
}

func TestAddProjectDetectionFailureStillCreates(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	failRun := func(dir, name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	srv := New(st, cfg, failRun)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	root := t.TempDir()
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	if p["warning"] == nil || p["warning"] == "" {
		t.Error("want warning in response when detection fails")
	}
	if len(p["channels"].([]any)) != 1 {
		t.Error("want general channel only")
	}
}

func TestPostAndReadMessages(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	mresp := postJSON(t, url, map[string]string{"kind": "message", "body": "hello"})
	if mresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", mresp.StatusCode)
	}
	m := decode[map[string]any](t, mresp)
	if m["author_kind"] != "human" {
		t.Errorf("no-token post should be human, got %v", m["author_kind"])
	}

	getResp, _ := http.Get(url + "?since=0&limit=10")
	msgs := decode[[]map[string]any](t, getResp)
	if len(msgs) != 1 || msgs[0]["body"] != "hello" {
		t.Fatalf("read back: %+v", msgs)
	}
}

func TestPostMessageWithRunToken(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	run, err := st.CreateRun(store.AgentRun{
		ChannelID: chID, Provider: "codex", AgentName: "impl-codex",
		Workdir: root, Status: "running", Spawner: "tmux",
	})
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	b, _ := json.Marshal(map[string]string{"kind": "report", "body": "done"})
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+run.Token)
	mresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	m := decode[map[string]any](t, mresp)
	if m["author_kind"] != "agent" || m["author_name"] != "impl-codex" {
		t.Errorf("agent attribution wrong: %+v", m)
	}

	req2, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer bogus")
	r2, _ := http.DefaultClient.Do(req2)
	if r2.StatusCode != http.StatusUnauthorized {
		t.Errorf("bogus token status = %d, want 401", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestPostMessageMultipartWithFile(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", "report")
	w.WriteField("body", "with artifact")
	fw, _ := w.CreateFormFile("file", "analysis.md")
	io.WriteString(fw, "# findings")
	w.Close()

	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	mresp, err := http.Post(url, w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if mresp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(mresp.Body)
		t.Fatalf("status = %d: %s", mresp.StatusCode, body)
	}
	m := decode[map[string]any](t, mresp)
	arts := m["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(arts))
	}
	if arts[0].(map[string]any)["filename"] != "analysis.md" {
		t.Errorf("artifact: %+v", arts[0])
	}
	if int64(arts[0].(map[string]any)["size"].(float64)) != int64(len("# findings")) {
		t.Error("artifact size mismatch")
	}
}

func TestValidation(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty repo_path status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
	resp2 := postJSON(t, ts.URL+"/api/channels/999/messages", map[string]string{"kind": "message", "body": "x"})
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unknown channel status = %d, want 404", resp2.StatusCode)
	}
	resp2.Body.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go get github.com/go-chi/chi/v5 && go test ./internal/server/ -v`
Expected: FAIL — undefined `New` (the `go get` first, so the failure is the right one)

- [ ] **Step 3: Implement server core**

`internal/server/server.go`:

```go
// Package server is the erbrus HTTP API and (in Plan 3) web UI. Localhost
// only; agents authenticate with per-run bearer tokens, the human needs
// none.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
	"erbrus/internal/store"
	"erbrus/internal/wt"
)

type Server struct {
	st      *store.Store
	cfg     config.Global
	run     wt.Runner
	dataDir string
}

func New(st *store.Store, cfg config.Global, run wt.Runner) *Server {
	return &Server{st: st, cfg: cfg, run: run, dataDir: cfg.ResolvedDataDir()}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Post("/projects", s.handleAddProject)
		r.Get("/projects", s.handleListProjects)
		r.Get("/channels/{id}/messages", s.handleGetMessages)
		r.Post("/channels/{id}/messages", s.handlePostMessage)
	})
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// bearerRun resolves the Authorization header to a run. ok=false with
// empty token means "no auth given" (human); an invalid token is an error.
func (s *Server) bearerRun(r *http.Request) (store.AgentRun, bool, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return store.AgentRun{}, false, true
	}
	token := strings.TrimPrefix(h, "Bearer ")
	run, ok, err := s.st.RunByToken(token)
	if err != nil || !ok {
		return store.AgentRun{}, false, false
	}
	return run, true, true
}
```

`internal/server/projects.go`:

```go
package server

import (
	"net/http"
	"path/filepath"

	"erbrus/internal/config"
	"erbrus/internal/store"
	"erbrus/internal/wt"
)

type channelJSON struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}

type projectJSON struct {
	ID       int64         `json:"id"`
	Name     string        `json:"name"`
	RepoPath string        `json:"repo_path"`
	Channels []channelJSON `json:"channels"`
	Created  bool          `json:"created"`
	Warning  string        `json:"warning,omitempty"`
}

func toChannelJSON(c store.Channel) channelJSON {
	return channelJSON{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, WorktreePath: c.WorktreePath, Branch: wt.ShortBranch(c.Branch)}
}

func (s *Server) projectJSON(p store.Project, created bool, warning string) (projectJSON, error) {
	chans, err := s.st.ChannelsByProject(p.ID)
	if err != nil {
		return projectJSON{}, err
	}
	pj := projectJSON{ID: p.ID, Name: p.Name, RepoPath: p.RepoPath, Created: created, Warning: warning, Channels: []channelJSON{}}
	for _, c := range chans {
		pj.Channels = append(pj.Channels, toChannelJSON(c))
	}
	return pj, nil
}

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoPath string `json:"repo_path"`
	}
	if err := decodeBody(r, &req); err != nil || req.RepoPath == "" {
		httpError(w, http.StatusBadRequest, "repo_path is required")
		return
	}
	root, err := filepath.Abs(config.ExpandHome(req.RepoPath))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if p, ok, err := s.st.ProjectByPath(root); err == nil && ok {
		pj, err := s.projectJSON(p, false, "")
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, pj)
		return
	}

	warning := ""
	wts, err := wt.List(s.run, s.cfg.WtBin, root)
	if err != nil {
		warning = "worktree detection failed: " + err.Error()
	}

	p, err := s.st.CreateProject(filepath.Base(root), root)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}

	repoCfg, _ := config.LoadRepo(root)
	perWorktree := repoCfg.ChannelPerWorktree == nil || *repoCfg.ChannelPerWorktree

	if _, err := s.st.CreateChannel(p.ID, "general", root, mainBranch(wts)); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if perWorktree {
		for _, w2 := range wts {
			if w2.IsMain {
				continue
			}
			name := wt.ShortBranch(w2.Branch)
			if name == "" {
				name = filepath.Base(w2.Path)
			}
			s.st.CreateChannel(p.ID, name, w2.Path, w2.Branch)
		}
	}

	pj, err := s.projectJSON(p, true, warning)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pj)
}

func mainBranch(wts []wt.Worktree) string {
	for _, w := range wts {
		if w.IsMain {
			return w.Branch
		}
	}
	return ""
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.st.Projects()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []projectJSON{}
	for _, p := range projects {
		pj, err := s.projectJSON(p, false, "")
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, pj)
	}
	writeJSON(w, http.StatusOK, out)
}
```

`internal/server/messages.go`:

```go
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/store"
)

type artifactJSON struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type messageJSON struct {
	ID              int64          `json:"id"`
	ChannelID       int64          `json:"channel_id"`
	Kind            string         `json:"kind"`
	AuthorKind      string         `json:"author_kind"`
	AuthorName      string         `json:"author_name"`
	OriginMessageID int64          `json:"origin_message_id,omitempty"`
	Body            string         `json:"body"`
	CreatedAt       string         `json:"created_at"`
	Artifacts       []artifactJSON `json:"artifacts,omitempty"`
}

func (s *Server) messageJSON(m store.Message) messageJSON {
	mj := messageJSON{
		ID: m.ID, ChannelID: m.ChannelID, Kind: m.Kind, AuthorKind: m.AuthorKind,
		AuthorName: m.AuthorName, OriginMessageID: m.OriginMessageID, Body: m.Body,
		CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	arts, _ := s.st.ArtifactsByMessage(m.ID)
	for _, a := range arts {
		mj.Artifacts = append(mj.Artifacts, artifactJSON{ID: a.ID, Filename: a.Filename, Size: a.Size})
	}
	return mj
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func chiInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, _ := s.st.ChannelByID(chID); !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := s.st.MessagesSince(chID, since, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []messageJSON{}
	for _, m := range msgs {
		out = append(out, s.messageJSON(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, _ := s.st.ChannelByID(chID); !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}

	run, isAgent, authOK := s.bearerRun(r)
	if !authOK {
		httpError(w, http.StatusUnauthorized, "invalid run token")
		return
	}

	var kind, body string
	var files []*multipart.FileHeader
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		kind = r.FormValue("kind")
		body = r.FormValue("body")
		if r.MultipartForm != nil {
			files = r.MultipartForm.File["file"]
		}
	} else {
		var req struct{ Kind, Body string }
		if err := decodeBody(r, &req); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		kind, body = req.Kind, req.Body
	}
	if kind == "" {
		kind = "message"
	}
	if kind != "message" && kind != "report" && kind != "system" {
		httpError(w, http.StatusBadRequest, "kind must be message|report|system")
		return
	}

	m := store.Message{ChannelID: chID, Kind: kind, Body: body}
	if isAgent {
		m.AuthorKind, m.AuthorName, m.AgentRunID = "agent", run.AgentName, run.ID
	} else {
		m.AuthorKind, m.AuthorName = "human", "you"
	}
	if kind == "system" {
		m.AuthorKind = "system"
	}

	saved, err := s.st.CreateMessage(m)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, f := range files {
		if err := s.saveArtifact(saved.ID, f); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, s.messageJSON(saved))
}

func (s *Server) saveArtifact(messageID int64, fh *multipart.FileHeader) error {
	dir := filepath.Join(s.dataDir, "artifacts", fmt.Sprint(messageID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Base(fh.Filename)
	dst := filepath.Join(dir, name)
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, src)
	if err != nil {
		return err
	}
	_, err = s.st.AddArtifact(store.Artifact{MessageID: messageID, Filename: name, Path: dst, Size: n})
	return err
}
```

Then: `go get github.com/go-chi/chi/v5`

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -v`
Expected: PASS (7 tests). If the multipart test fails on size, check `io.Copy` return value is used for `Size`.

- [ ] **Step 5: Commit**

```bash
git add internal/server/ go.mod go.sum
git commit -m "feat: HTTP API for projects, channels, messages, artifacts"
```

---

### Task 8: Forward endpoint + SSE hub (internal/server)

**Files:**
- Create: `internal/server/sse.go`, `internal/server/forward.go`
- Modify: `internal/server/server.go` (register routes, create hub, publish on message create)
- Test: `internal/server/sse_test.go`

**Interfaces:**
- Consumes: Task 7's server internals.
- Produces:

```go
type Hub struct { /* private: mu, subscribers map[chan event]struct{} */ }
func NewHub() *Hub
func (h *Hub) Publish(event string, data any)          // JSON-encodes data
func (h *Hub) Subscribe() (ch <-chan sseEvent, cancel func())
// New routes:
// POST /api/messages/{id}/forward  {"channel_id": N} -> 201 messageJSON (the copy)
// GET  /events                     -> SSE stream; events: "message" (messageJSON), later "run" (Plan 2)
```

Forward semantics (spec): copy body/kind/author into the target channel with `OriginMessageID` = source id; artifacts are **shared, not duplicated** — the copy's rendered artifacts come from the origin message (Plan 3 renders via origin); v1 API response for the copy includes the origin's artifacts. Server.New wires `hub *Hub`; `handlePostMessage` and `handleForward` call `s.hub.Publish("message", mj)` after creation.

- [ ] **Step 1: Write the failing tests**

`internal/server/sse_test.go`:

```go
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestForward(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))

	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src),
		map[string]string{"kind": "report", "body": "the report"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	fresp := postJSON(t, fmt.Sprintf("%s/api/messages/%d/forward", ts.URL, msgID),
		map[string]int64{"channel_id": dst})
	if fresp.StatusCode != http.StatusCreated {
		t.Fatalf("forward status = %d", fresp.StatusCode)
	}
	copyMsg := decode[map[string]any](t, fresp)
	if int64(copyMsg["origin_message_id"].(float64)) != msgID {
		t.Error("copy must reference origin")
	}
	if int64(copyMsg["channel_id"].(float64)) != dst {
		t.Error("copy in wrong channel")
	}
	if copyMsg["body"] != "the report" {
		t.Error("body not copied")
	}
	_ = st
}

func TestForwardUnknownTargets(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	src := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src),
		map[string]string{"kind": "report", "body": "x"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	r1 := postJSON(t, fmt.Sprintf("%s/api/messages/%d/forward", ts.URL, msgID), map[string]int64{"channel_id": 999})
	if r1.StatusCode != http.StatusNotFound {
		t.Errorf("bad channel: %d, want 404", r1.StatusCode)
	}
	r1.Body.Close()
	r2 := postJSON(t, ts.URL+"/api/messages/999/forward", map[string]int64{"channel_id": src})
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("bad message: %d, want 404", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestSSEDeliversMessageEvent(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	req, _ := http.NewRequest("GET", ts.URL+"/events", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	if ct := sseResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	got := make(chan map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(sseResp.Body)
		var event string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") && event == "message" {
				var v map[string]any
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v)
				got <- v
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond) // let the subscription register
	postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID),
		map[string]string{"kind": "message", "body": "live"}).Body.Close()

	select {
	case v := <-got:
		if v["body"] != "live" {
			t.Errorf("event body = %v", v["body"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE event within 3s")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'Forward|SSE' -v`
Expected: FAIL — 404s on missing routes / undefined Hub

- [ ] **Step 3: Implement**

`internal/server/sse.go`:

```go
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type sseEvent struct {
	Event string
	Data  []byte
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan sseEvent]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan sseEvent]struct{}{}} }

func (h *Hub) Publish(event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- sseEvent{Event: event, Data: b}:
		default: // slow subscriber: drop rather than block the server
		}
	}
}

func (h *Hub) Subscribe() (<-chan sseEvent, func()) {
	ch := make(chan sseEvent, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ch, cancel := s.hub.Subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			fl.Flush()
		}
	}
}
```

`internal/server/forward.go`:

```go
package server

import (
	"net/http"

	"erbrus/internal/store"
)

func (s *Server) handleForward(w http.ResponseWriter, r *http.Request) {
	msgID := chiInt64(r, "id")
	src, ok, err := s.st.MessageByID(msgID)
	if err != nil || !ok {
		httpError(w, http.StatusNotFound, "message not found")
		return
	}
	var req struct {
		ChannelID int64 `json:"channel_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok, _ := s.st.ChannelByID(req.ChannelID); !ok {
		httpError(w, http.StatusNotFound, "target channel not found")
		return
	}
	cp, err := s.st.CreateMessage(store.Message{
		ChannelID:       req.ChannelID,
		Kind:            src.Kind,
		AuthorKind:      src.AuthorKind,
		AuthorName:      src.AuthorName,
		OriginMessageID: src.ID,
		Body:            src.Body,
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mj := s.messageJSON(cp)
	// Artifacts live with the origin; surface them on the copy.
	origin := s.messageJSON(src)
	mj.Artifacts = origin.Artifacts
	s.hub.Publish("message", mj)
	writeJSON(w, http.StatusCreated, mj)
}
```

Modify `internal/server/server.go`: add `hub *Hub` field; in `New`: `hub: NewHub()`; register routes:

```go
	r.Post("/api/messages/{id}/forward", s.handleForward)   // inside the /api Route block as r.Post("/messages/{id}/forward", ...)
	r.Get("/events", s.handleEvents)                        // top-level, outside /api
```

Modify `handlePostMessage` (messages.go): after the artifact loop, before `writeJSON`:

```go
	mj := s.messageJSON(saved)
	s.hub.Publish("message", mj)
	writeJSON(w, http.StatusCreated, mj)
```

- [ ] **Step 4: Run all server tests**

Run: `go test ./internal/server/ -v`
Expected: PASS (10 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat: message forwarding and SSE event stream"
```

---

### Task 9: API client + `msg send` / `msg read` subcommands

**Files:**
- Create: `internal/client/client.go`, `internal/cli/msg.go`
- Modify: `internal/cli/cli.go` (dispatch `msg`)
- Test: `internal/client/client_test.go`, `internal/cli/msg_test.go`

**Interfaces:**
- Consumes: server JSON envelopes (Task 7/8), env vars `ERBRUS_URL`, `ERBRUS_TOKEN`, `ERBRUS_CHANNEL`.
- Produces:

```go
package client
type Message struct {              // mirror of server messageJSON
	ID              int64      `json:"id"`
	ChannelID       int64      `json:"channel_id"`
	Kind            string     `json:"kind"`
	AuthorKind      string     `json:"author_kind"`
	AuthorName      string     `json:"author_name"`
	OriginMessageID int64      `json:"origin_message_id,omitempty"`
	Body            string     `json:"body"`
	CreatedAt       string     `json:"created_at"`
	Artifacts       []Artifact `json:"artifacts,omitempty"`
}
type Artifact struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}
type Channel struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}
type Project struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	RepoPath string    `json:"repo_path"`
	Channels []Channel `json:"channels"`
	Created  bool      `json:"created"`
	Warning  string    `json:"warning,omitempty"`
}
type Client struct { Base, Token string; HTTP *http.Client }
func New(base, token string) *Client
func FromEnv() (*Client, int64, error)   // uses ERBRUS_URL (default http://127.0.0.1:7420), ERBRUS_TOKEN, ERBRUS_CHANNEL; returns channel id
func (c *Client) SendMessage(channelID int64, kind, body string, files []string) (Message, error)  // multipart when files present
func (c *Client) ReadMessages(channelID, since int64, limit int) ([]Message, error)
func (c *Client) AddProject(repoPath string) (Project, error)
func (c *Client) ListProjects() ([]Project, error)
```

CLI (spec):

```
erbrus msg send [--report] [--system] [--file <path>] [--channel <id>] <text | - reads stdin>
erbrus msg read [--since <id>] [--limit N] [--channel <id>]
```

`--channel` overrides `ERBRUS_CHANNEL`. `msg read` prints one line per message: `[id] author (kind): body` plus `  ↳ file: name (size)` per artifact.

- [ ] **Step 1: Write the failing client tests**

`internal/client/client_test.go` (spin the real server via httptest — reuse the pattern from server tests):

```go
package client

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/server"
	"erbrus/internal/store"
)

func testServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()
	run := func(dir, name string, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, root)), nil
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	ts := httptest.NewServer(server.New(st, cfg, run).Handler())
	t.Cleanup(ts.Close)
	return ts, root
}

func TestClientRoundtrip(t *testing.T) {
	ts, root := testServer(t)
	c := New(ts.URL, "")

	p, err := c.AddProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Created || len(p.Channels) != 1 {
		t.Fatalf("AddProject: %+v", p)
	}
	ch := p.Channels[0].ID

	art := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(art, []byte("hello notes"), 0o644)
	sent, err := c.SendMessage(ch, "report", "did the thing", []string{art})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Kind != "report" || len(sent.Artifacts) != 1 || sent.Artifacts[0].Filename != "notes.md" {
		t.Fatalf("SendMessage: %+v", sent)
	}

	msgs, err := c.ReadMessages(ch, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Body != "did the thing" {
		t.Fatalf("ReadMessages: %+v", msgs)
	}

	list, err := c.ListProjects()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProjects: %v %+v", err, list)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:9")
	t.Setenv("ERBRUS_TOKEN", "tok")
	t.Setenv("ERBRUS_CHANNEL", "42")
	c, ch, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "http://127.0.0.1:9" || c.Token != "tok" || ch != 42 {
		t.Fatalf("FromEnv: %+v ch=%d", c, ch)
	}
	t.Setenv("ERBRUS_URL", "")
	c, _, _ = FromEnv()
	if c.Base != "http://127.0.0.1:7420" {
		t.Errorf("default base = %q", c.Base)
	}
}

func TestSendMessageServerError(t *testing.T) {
	ts, _ := testServer(t)
	c := New(ts.URL, "")
	if _, err := c.SendMessage(999, "message", "x", nil); err == nil {
		t.Fatal("want error for unknown channel")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/client/ -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement the client**

`internal/client/client.go`:

```go
// Package client talks to a running erbrus server over its HTTP API. Both
// the CLI subcommands and spawned agents (via env vars) use it.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type Artifact struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type Message struct {
	ID              int64      `json:"id"`
	ChannelID       int64      `json:"channel_id"`
	Kind            string     `json:"kind"`
	AuthorKind      string     `json:"author_kind"`
	AuthorName      string     `json:"author_name"`
	OriginMessageID int64      `json:"origin_message_id,omitempty"`
	Body            string     `json:"body"`
	CreatedAt       string     `json:"created_at"`
	Artifacts       []Artifact `json:"artifacts,omitempty"`
}

type Channel struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}

type Project struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	RepoPath string    `json:"repo_path"`
	Channels []Channel `json:"channels"`
	Created  bool      `json:"created"`
	Warning  string    `json:"warning,omitempty"`
}

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func New(base, token string) *Client {
	return &Client{Base: base, Token: token, HTTP: http.DefaultClient}
}

func FromEnv() (*Client, int64, error) {
	base := os.Getenv("ERBRUS_URL")
	if base == "" {
		base = "http://127.0.0.1:7420"
	}
	ch, _ := strconv.ParseInt(os.Getenv("ERBRUS_CHANNEL"), 10, 64)
	return New(base, os.Getenv("ERBRUS_TOKEN")), ch, nil
}

func (c *Client) do(method, path string, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.Base+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("erbrus server unreachable at %s (is `erbrus serve` running?): %w", c.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do("POST", path, "application/json", bytes.NewReader(b), out)
}

func (c *Client) SendMessage(channelID int64, kind, body string, files []string) (Message, error) {
	var m Message
	path := fmt.Sprintf("/api/channels/%d/messages", channelID)
	if len(files) == 0 {
		err := c.postJSON(path, map[string]string{"kind": kind, "body": body}, &m)
		return m, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", kind)
	w.WriteField("body", body)
	for _, f := range files {
		fw, err := w.CreateFormFile("file", filepath.Base(f))
		if err != nil {
			return m, err
		}
		src, err := os.Open(f)
		if err != nil {
			return m, err
		}
		if _, err := io.Copy(fw, src); err != nil {
			src.Close()
			return m, err
		}
		src.Close()
	}
	w.Close()
	err := c.do("POST", path, w.FormDataContentType(), &buf, &m)
	return m, err
}

func (c *Client) ReadMessages(channelID, since int64, limit int) ([]Message, error) {
	var msgs []Message
	err := c.do("GET", fmt.Sprintf("/api/channels/%d/messages?since=%d&limit=%d", channelID, since, limit), "", nil, &msgs)
	return msgs, err
}

func (c *Client) AddProject(repoPath string) (Project, error) {
	var p Project
	err := c.postJSON("/api/projects", map[string]string{"repo_path": repoPath}, &p)
	return p, err
}

func (c *Client) ListProjects() ([]Project, error) {
	var ps []Project
	err := c.do("GET", "/api/projects", "", nil, &ps)
	return ps, err
}
```

- [ ] **Step 4: Run client tests**

Run: `go test ./internal/client/ -v`
Expected: PASS

- [ ] **Step 5: Write the failing CLI tests**

`internal/cli/msg_test.go`:

```go
package cli

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/server"
	"erbrus/internal/store"
)

func msgTestServer(t *testing.T) (string, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	run := func(dir, name string, args ...string) ([]byte, error) { return nil, fmt.Errorf("no") }
	ts := httptest.NewServer(server.New(st, cfg, run).Handler())
	t.Cleanup(ts.Close)
	p, _ := st.CreateProject("x", t.TempDir())
	c, _ := st.CreateChannel(p.ID, "general", "", "")
	return ts.URL, c.ID
}

func TestMsgSendAndRead(t *testing.T) {
	url, ch := msgTestServer(t)
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_CHANNEL", fmt.Sprint(ch))
	t.Setenv("ERBRUS_TOKEN", "")

	var out, errOut bytes.Buffer
	code := Run([]string{"msg", "send", "hello world"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("send exit = %d, stderr: %s", code, errOut.String())
	}

	out.Reset()
	code = Run([]string{"msg", "read"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("read exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hello world") {
		t.Errorf("read output %q", out.String())
	}
	if !strings.Contains(out.String(), "you") {
		t.Errorf("author missing in %q", out.String())
	}
}

func TestMsgSendReportFlag(t *testing.T) {
	url, ch := msgTestServer(t)
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_CHANNEL", fmt.Sprint(ch))

	var out, errOut bytes.Buffer
	if code := Run([]string{"msg", "send", "--report", "findings"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	out.Reset()
	Run([]string{"msg", "read"}, &out, &errOut)
	if !strings.Contains(out.String(), "(report)") {
		t.Errorf("read output %q, want kind report shown", out.String())
	}
}

func TestMsgSendNoChannel(t *testing.T) {
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:1")
	t.Setenv("ERBRUS_CHANNEL", "")
	var out, errOut bytes.Buffer
	if code := Run([]string{"msg", "send", "x"}, &out, &errOut); code == 0 {
		t.Fatal("want non-zero exit without channel")
	}
	if !strings.Contains(errOut.String(), "channel") {
		t.Errorf("stderr %q should mention channel", errOut.String())
	}
}
```

- [ ] **Step 6: Run to verify failure, then implement**

Run: `go test ./internal/cli/ -v` — FAIL (msg unknown command).

`internal/cli/msg.go`:

```go
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"erbrus/internal/client"
)

func runMsg(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: erbrus msg <send|read> [flags]")
		return 2
	}
	switch args[0] {
	case "send":
		return runMsgSend(args[1:], stdin, stdout, stderr)
	case "read":
		return runMsgRead(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown msg subcommand %q\n", args[0])
		return 2
	}
}

func runMsgSend(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("msg send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	report := fs.Bool("report", false, "post as a report")
	system := fs.Bool("system", false, "post as a system note")
	file := fs.String("file", "", "attach a file as artifact")
	channel := fs.Int64("channel", 0, "channel id (overrides ERBRUS_CHANNEL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, envCh, _ := client.FromEnv()
	ch := envCh
	if *channel != 0 {
		ch = *channel
	}
	if ch == 0 {
		fmt.Fprintln(stderr, "no channel: set ERBRUS_CHANNEL or pass --channel <id>")
		return 2
	}
	body := strings.Join(fs.Args(), " ")
	if body == "-" || body == "" {
		b, _ := io.ReadAll(stdin)
		body = strings.TrimSpace(string(b))
	}
	if body == "" {
		fmt.Fprintln(stderr, "empty message")
		return 2
	}
	kind := "message"
	if *report {
		kind = "report"
	}
	if *system {
		kind = "system"
	}
	var files []string
	if *file != "" {
		files = []string{*file}
	}
	m, err := c.SendMessage(ch, kind, body, files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "sent message %d to channel %d\n", m.ID, m.ChannelID)
	return 0
}

func runMsgRead(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("msg read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.Int64("since", 0, "only messages after this id")
	limit := fs.Int("limit", 50, "max messages")
	channel := fs.Int64("channel", 0, "channel id (overrides ERBRUS_CHANNEL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, envCh, _ := client.FromEnv()
	ch := envCh
	if *channel != 0 {
		ch = *channel
	}
	if ch == 0 {
		fmt.Fprintln(stderr, "no channel: set ERBRUS_CHANNEL or pass --channel <id>")
		return 2
	}
	msgs, err := c.ReadMessages(ch, *since, *limit)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, m := range msgs {
		fmt.Fprintf(stdout, "[%d] %s (%s): %s\n", m.ID, m.AuthorName, m.Kind, m.Body)
		for _, a := range m.Artifacts {
			fmt.Fprintf(stdout, "  ↳ file: %s (%d bytes)\n", a.Filename, a.Size)
		}
	}
	return 0
}
```

`Run` in `cli.go` gains:

```go
	case "msg":
		return runMsg(args[1:], os.Stdin, stdout, stderr)
```

(import `os` in cli.go; stdin is acceptable as a direct dependency here).

- [ ] **Step 7: Run all tests**

Run: `go test ./... `
Expected: PASS across all packages

- [ ] **Step 8: Commit**

```bash
git add internal/client/ internal/cli/
git commit -m "feat: API client and msg send/read subcommands"
```

---

### Task 10: `serve` and `init` subcommands + binary handoff

**Files:**
- Create: `internal/cli/serve.go`, `internal/cli/initcmd.go`
- Modify: `internal/cli/cli.go` (dispatch `serve`, `init`)
- Test: `internal/cli/serve_test.go`

**Interfaces:**
- Consumes: `config.LoadGlobal`/`GlobalPath`, `store.Open`, `server.New`, `client.*`, `wt.ExecRunner`/`GitRoot`/`MainRoot`.
- Produces: the complete Plan-1 binary. `serve` listens on `127.0.0.1:<port>`; `init` auto-configures the current repo and scaffolds `.erbrus.yaml`.

- [ ] **Step 1: Write the failing tests**

`internal/cli/serve_test.go`:

```go
package cli

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeStartsAndServes boots serve on a random port in a goroutine and
// hits /api/projects.
func TestServeStartsAndServes(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 0\ndata_dir: "+tmp+"\n"), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfgPath)

	addrCh := make(chan string, 1)
	serveAddrHook = func(addr string) { addrCh <- addr }
	defer func() { serveAddrHook = nil }()

	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"serve"}, &out, &errOut)
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not report an address")
	}
	resp, err := http.Get("http://" + addr + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestInitAutoConfigures runs init inside a real temp git repo against a
// live serve instance.
func TestInitAutoConfigures(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "myrepo")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"commit", "--allow-empty", "-m", "x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 0\ndata_dir: "+tmp+"\n"), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfgPath)

	addrCh := make(chan string, 1)
	serveAddrHook = func(addr string) { addrCh <- addr }
	defer func() { serveAddrHook = nil }()
	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"serve"}, &out, &errOut)
	}()
	addr := <-addrCh
	t.Setenv("ERBRUS_URL", "http://"+addr)

	oldWd, _ := os.Getwd()
	os.Chdir(repo)
	defer os.Chdir(oldWd)

	var out, errOut bytes.Buffer
	code := Run([]string{"init"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("init exit = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "myrepo") {
		t.Errorf("output %q should announce project name", out.String())
	}
	if !strings.Contains(out.String(), "general") {
		t.Errorf("output %q should announce general channel", out.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".erbrus.yaml")); err != nil {
		t.Error(".erbrus.yaml not scaffolded")
	}

	// Second init: idempotent, no scaffold overwrite.
	os.WriteFile(filepath.Join(repo, ".erbrus.yaml"), []byte("session: custom\n"), 0o644)
	out.Reset()
	if code := Run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("second init failed: %s", errOut.String())
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".erbrus.yaml"))
	if string(data) != "session: custom\n" {
		t.Error("init must not overwrite an existing .erbrus.yaml")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cli/ -run 'Serve|Init' -v`
Expected: FAIL — unknown commands / undefined `serveAddrHook`

- [ ] **Step 3: Implement**

`internal/cli/serve.go`:

```go
package cli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"erbrus/internal/config"
	"erbrus/internal/server"
	"erbrus/internal/store"
	"erbrus/internal/wt"
)

// serveAddrHook, when set (tests), receives the bound address.
var serveAddrHook func(addr string)

// configPath honors ERBRUS_CONFIG for tests; defaults to the XDG location.
func configPath() string {
	if p := os.Getenv("ERBRUS_CONFIG"); p != "" {
		return p
	}
	return config.GlobalPath()
}

func runServe(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.LoadGlobal(configPath())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dataDir := cfg.ResolvedDataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	st, err := store.Open(filepath.Join(dataDir, "erbrus.db"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer st.Close()

	srv := server.New(st, cfg, wt.ExecRunner)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "erbrus serving on http://%s (data: %s)\n", ln.Addr(), dataDir)
	if serveAddrHook != nil {
		serveAddrHook(ln.Addr().String())
	}
	if err := http.Serve(ln, srv.Handler()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
```

`internal/cli/initcmd.go`:

```go
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"erbrus/internal/client"
	"erbrus/internal/wt"
)

const scaffoldYAML = `# erbrus per-repo settings. Everything is optional; global config fills gaps.
# session: erbrus-myproject
# attach_session: ""
# channel_per_worktree: true
# presets:
#   claude:
#     provider: claude-code
`

// runInit registers the repo containing the cwd as a project (auto-configure)
// and scaffolds .erbrus.yaml. Spawning is Plan 2; init never spawns.
func runInit(args []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root, err := wt.MainRoot(wt.ExecRunner, cwd)
	if err != nil {
		fmt.Fprintln(stderr, "not inside a git repository:", err)
		return 1
	}

	c, _, _ := client.FromEnv()
	p, err := c.AddProject(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if p.Created {
		fmt.Fprintf(stdout, "created project %s (%s)\n", p.Name, p.RepoPath)
	} else {
		fmt.Fprintf(stdout, "project %s already registered (%s)\n", p.Name, p.RepoPath)
	}
	for _, ch := range p.Channels {
		fmt.Fprintf(stdout, "  channel #%s (id %d)\n", ch.Name, ch.ID)
	}
	if p.Warning != "" {
		fmt.Fprintln(stdout, "warning:", p.Warning)
	}

	scaffold := filepath.Join(root, ".erbrus.yaml")
	if _, err := os.Stat(scaffold); os.IsNotExist(err) {
		if err := os.WriteFile(scaffold, []byte(scaffoldYAML), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "scaffolded %s\n", scaffold)
	}
	return 0
}
```

Wire into `Run` in `cli.go`:

```go
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS, no vet complaints

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat: serve and init subcommands"
```

- [ ] **Step 6: Build the binary and hand off (Global Constraint 2)**

```bash
mkdir -p bin && go build -o bin/erbrus ./cmd/erbrus
```

Tell the user the absolute path of `bin/erbrus` inside the worktree, plus this manual test script:

```bash
# terminal 1
<worktree>/bin/erbrus serve
# terminal 2, inside any git repo
<worktree>/bin/erbrus init
ERBRUS_CHANNEL=1 <worktree>/bin/erbrus msg send "hello from the shell"
ERBRUS_CHANNEL=1 <worktree>/bin/erbrus msg send --report --file README.md "first report"
ERBRUS_CHANNEL=1 <worktree>/bin/erbrus msg read
curl -N http://127.0.0.1:7420/events   # then send another message, watch it stream
```

Do NOT merge to main. Do not push. Stop here and wait for the user's manual-test verdict.

---

## Self-review notes (already applied)

- Spec coverage for Plan 1 scope: config ✔ (Task 2), providers/presets ✔ (3, 4), wt detection incl. fallback + failure warning ✔ (5, 7), store incl. runs schema for Plan 2 ✔ (6), API + tokens + artifacts ✔ (7), forward + SSE ✔ (8), CLI msg ✔ (9), serve/init/auto-configure/scaffold ✔ (10). Spawning, hooks, `start`, web UI: Plans 2–3 by design.
- Type consistency: `wt.Runner` signature used identically in Tasks 5, 7, 10; JSON envelopes in Task 9 mirror Task 7 field-for-field; `store.AgentRun` fields match the Task 6 schema.
- Known simplifications, intentional: single-writer SQLite (`SetMaxOpenConns(1)`); SSE has no replay/reconnect state (clients re-fetch via `since`); `init` uses `wt.MainRoot` so running it from a worktree registers the main repo (channels for worktrees come from detection).
- Parked for Plan 2: `erbrus start` will want `--channel` to accept a channel *name* (resolved via project context), not just an id; server-side handoff prompt-building reads artifact files by `store.Artifact.Path` directly.
- Parked for Plan 3: no artifact-download endpoint exists yet — the UI's artifact chips need `GET /api/artifacts/{id}` added then.

---

## Post-execution carry-overs to Plan 2 (from final whole-branch review, 2026-08-27)

Executed on branch `plan1-core` (700838d..2434d9c), 54 tests green under -race. Deferred to Plan 2's first server-touching task:

1. Project-name collision: two repos with the same directory basename hit `projects.name UNIQUE` → raw 500. Detect and suffix the name or return 409.
2. Remaining store-error collapses: ChannelByID/MessageByID errors map to 404 (messages, forward) and bearerRun maps store errors to 401 — return 500 on real errors.
3. Plan 2's injected agent instructions should note stdlib-flag ordering: flags before positionals (`erbrus msg send --report "text"`).
4. Timestamps are stored/served as UTC-naive strings — convert at render time in Plan 3.
5. **Per-repo config relocation (user request, 2026-08-27):** per-repo config moves out of the
   repo to `~/.erbrus/repos/<encoded-repo-path>/.erbrus.yaml`, where the encoding replaces every
   `/` of the repo's absolute path with `-` keeping the leading dash (Claude's project-dir scheme:
   `/home/homeend/git-focus` → `-home-homeend-git-focus`). Touches: `config.LoadRepo` (new path
   resolution + `EncodeRepoPath(path string) string` helper), `server.handleAddProject`
   (`config.LoadRepo(root)` call semantics unchanged — the function resolves the new location
   itself), `cli.runInit` (scaffold at the new location, never in the repo). Spec already updated.
   Migration nicety: if a legacy `<repo>/.erbrus.yaml` exists and the new file doesn't, read the
   legacy one and print a deprecation note suggesting `erbrus init` to migrate.
