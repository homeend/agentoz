# Worktree Creation From The Sidebar — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "+" next to each project in the sidebar creates a git worktree through `gg` (existing branch, or new branch from a base) and lands the user in the new channel; on failure it shows gg's message and offers a shell terminal in the main repository.

**Architecture:** A new `internal/gg` package wraps the three gg commands behind an injectable runner (same shape as `internal/wt`). Providers gain a `type` (`agent`|`tool`); a built-in `shell` tool provider is merged into every loaded config, and `spawnRunCore` skips preamble/prompt/paste/classification for tool runs. A new `ui_worktree.go` serves the form, runs gg, re-syncs channels and redirects; its failure page carries a button that spawns a `shell` run in the project's main channel and opens its terminal.

**Tech Stack:** Go 1.2x, chi router, html/template, yaml.v3, SQLite store; tests with `httptest` and the existing `fakeSpawner`; gg 26.x binary on PATH for the live check only.

**Spec:** `docs/superpowers/specs/2026-09-10-worktree-create-design.md`

## Global Constraints

- Work happens in a native worktree under `.claude/worktrees` (branch `worktree-create`); merge to `main` ff-only only when the user says so. Never push.
- Inside a worktree the Bash guard refuses commands containing `git`, heredocs, `-C` and compound constructs: run git as `/usr/bin/git`, write files with the Write tool, put multi-line scripts in the scratchpad.
- Commit after each task; run `./test.sh` (gofmt + vet + `go test -count=1 ./...`) before every commit. Commit messages end with the session's attribution trailer.
- The erbrus CLI talks to `http://127.0.0.1:7420` (the user's live server) unless `ERBRUS_URL` is set. Every CLI call in a live check MUST set `ERBRUS_URL=http://127.0.0.1:7499` and use the throwaway instance (scratchpad `start7499.sh`). Never kill the user's tmux server, their `erbrus serve`, or any window not named `claudetest-*`.
- gg facts (verified 2026-09-10, spec table): existing branch = `gg worktree add --branch <name>`; new branch = `gg worktree add --from <base> <name>`; NEVER the bare `gg worktree add <branch>` (creates `<branch>-<date>`). Success line: `✓ created worktree <branch> at <path>`. Errors: `error: create worktree: …`, exit 1.
- Channel naming stays the existing rule in `syncChannels`: `wt.ShortBranch(branch)`, falling back to the directory basename (spec amendment: the spec said "directory"; the code names by short branch, and that stays).
- Type-keyed behaviour: every "is this a tool run" check reads `Provider.Type`, never the provider name.
- Timeout for gg: 60 s, stdin closed, stderr merged into the captured output.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/config/config.go` | `Provider.Type`, `Provider.IsTool()`, `Global.GgBin`, built-in `shell` provider + preset merged in `LoadGlobal`/`Defaults` |
| `internal/config/config_shell_test.go` | defaults, override, merge behaviour |
| `internal/config/screenrules.go` | `ProviderPatch.Type` written by `SetProvider` |
| `internal/server/api_providers.go` | `type` in GET/PUT JSON, validation |
| `internal/cli/provider.go` | `provider set --type` |
| `internal/server/config_live.go` | `ggBin()` accessor; `ReloadConfig` swaps `GgBin` |
| `internal/gg/gg.go`, `gg_test.go` | branch list, worktree list, the two add commands, error message extraction, real runner |
| `internal/server/runs.go` | tool-run branch in `spawnRunCore` |
| `internal/server/watch.go` | skip classification for tool runs |
| `internal/server/ui_channel.go` | `runView.Tool`; composer target filter; `sendToRun` refusal |
| `internal/server/ui_forward.go` | forward target filter |
| `internal/server/tool_run_test.go` | tool-run behaviour end to end |
| `internal/server/ui_worktree.go`, `ui_worktree_test.go` | form GET/POST, shell POST |
| `internal/server/server.go` | three routes |
| `internal/web/templates/worktree.html` | the form page |
| `internal/web/templates/channel.html` | sidebar `+` |
| `internal/web/templates/_runs.html` | `shell` badge, no state badge for tool runs |
| `internal/web/static/app.css` | `.proj .plus`, `.badge.tool` |
| `docs/superpowers/specs/2026-09-10-worktree-create-design.md` | amendments |
| `docs/BACKLOG.md` | verification items |

---

### Task 0: Worktree

**Files:** none (workspace only)

- [ ] **Step 1: Create the isolated workspace** with the native `EnterWorktree` tool, branch name `worktree-create`. Confirm with `/usr/bin/git branch --show-current` → `worktree-create`.
- [ ] **Step 2: Baseline** — run `./test.sh`. Expected: `OK`. If not, stop and report.

---

### Task 1: Provider type, gg_bin, built-in shell provider

**Files:**
- Modify: `internal/config/config.go` (Provider struct ~48-62, Global ~71-83, `Defaults` ~91-101, `LoadGlobal` ~114-140)
- Modify: `internal/config/screenrules.go:33-36` (ProviderPatch), `:63-70` (scalar loop)
- Modify: `internal/server/api_providers.go:120-129` (providerJSON), `:160-166` (patch), the PUT handler validation
- Modify: `internal/cli/provider.go:43-80` (`--type` flag)
- Modify: `internal/server/config_live.go` (`ggBin()`, `ReloadConfig`)
- Create: `internal/config/config_shell_test.go`
- Modify: `internal/server/config_reload_test.go` (one case for `gg_bin`)

**Interfaces:**
- Produces: `config.Provider.Type string` (yaml `type`), `func (p Provider) IsTool() bool` (true iff `Type == "tool"`), `config.ProviderTypes = []string{"agent","tool"}`, `config.Global.GgBin string` (yaml `gg_bin`, default `"gg"`), `config.ShellProvider() Provider` (`Type: "tool", Command: "${SHELL:-bash}"`), `config.ShellPreset() Preset` (`Provider: "shell"`), `func (s *Server) ggBin() string`.
- Rule: `Defaults()` includes `Providers["shell"] = ShellProvider()` and `Presets["shell"] = ShellPreset()`; after `LoadGlobal` unmarshals, both are re-added when absent (a user entry of the same name wins).

- [ ] **Step 1: Write the failing config tests**

`internal/config/config_shell_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ -run 'Shell|WritesType' -v`
Expected: compile errors (`IsTool`, `GgBin`, `ProviderPatch.Type` undefined).

- [ ] **Step 3: Implement config**

In `internal/config/config.go`, Provider struct — add after `Command`:

```go
	// Type is what the provider is: "agent" (default, a coding agent that
	// takes a prompt and reports) or "tool" (a terminal program for the
	// human — no preamble, no prompt, no screen classification).
	Type string `yaml:"type"`
```

after the struct:

```go
// ProviderTypes are the accepted values of Provider.Type ("" means agent).
var ProviderTypes = []string{"agent", "tool"}

// IsTool reports whether the provider is a human-facing terminal program
// rather than a coding agent.
func (p Provider) IsTool() bool { return p.Type == "tool" }

// ShellProvider is the built-in "shell" tool provider: the user's login
// shell, expanded by cmd.sh at exec time so the tmux server's SHELL wins.
func ShellProvider() Provider { return Provider{Type: "tool", Command: "${SHELL:-bash}"} }

// ShellPreset is the preset that spawns ShellProvider under its own name.
func ShellPreset() Preset { return Preset{Provider: "shell"} }
```

Global struct — add after `WtBin`:

```go
	// GgBin is the gigagit binary erbrus runs for worktree creation.
	GgBin string `yaml:"gg_bin"`
```

`Defaults()` — add `GgBin: "gg",` and replace the two empty maps:

```go
		Providers:      map[string]Provider{"shell": ShellProvider()},
		Presets:        map[string]Preset{"shell": ShellPreset()},
```

`LoadGlobal` — replace the two nil-map guards with:

```go
	if g.Providers == nil {
		g.Providers = map[string]Provider{}
	}
	if g.Presets == nil {
		g.Presets = map[string]Preset{}
	}
	// Built-ins survive a file that lists its own providers/presets; a
	// user entry of the same name wins.
	if _, ok := g.Providers["shell"]; !ok {
		g.Providers["shell"] = ShellProvider()
	}
	if _, ok := g.Presets["shell"]; !ok {
		g.Presets["shell"] = ShellPreset()
	}
	if g.GgBin == "" {
		g.GgBin = "gg"
	}
```

`internal/config/screenrules.go` — `ProviderPatch` gains `Type *string` (put it on the first line: `Command, DefaultModel, Prompt, Type *string`), and the scalar loop gains `{"type", p.Type}` after `{"prompt", p.Prompt}`.

- [ ] **Step 4: API and CLI**

`internal/server/api_providers.go`: add `Type string \`json:"type"\`` to `providerJSON` (after `Command`) and set `Type: p.Type` in `providerToJSON`; add `Type *string \`json:"type"\`` to `providerPatchJSON`; in `handleAPIPutProvider` after the prompt validation:

```go
	if req.Type != nil && *req.Type != "" && *req.Type != "agent" && *req.Type != "tool" {
		httpError(w, http.StatusUnprocessableEntity, `type must be "agent" or "tool"`)
		return
	}
```

and `if req.Type != nil { next.Type = *req.Type }` next to the Command assignment. Find where the handler builds the `config.ProviderPatch` for the file rewrite (search `ProviderPatch{` in this file) and pass `Type: req.Type`.

`internal/cli/provider.go` `set`: `ptype := fs.String("type", "", "agent|tool")`; `if visited["type"] { patch["type"] = *ptype }`. In `printProvider` (same file) print `type: <value or agent>` on the line after `command:`.

- [ ] **Step 5: Live reload**

`internal/server/config_live.go`: add

```go
func (s *Server) ggBin() string {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return s.cfg.GgBin
}
```

In `ReloadConfig` extend the equality check with `&& cur.GgBin == next.GgBin` and the swap with `s.cfg.GgBin = next.GgBin` (same statement). Update the file's header comment to list gg_bin.

Add to `internal/server/config_reload_test.go` a case modelled on the existing wt_bin case (read that file first): write `gg_bin: /x/gg` into the config path, call `ReloadConfig`, expect `changed == true` and `testSrv.ggBin() == "/x/gg"`.

- [ ] **Step 6: Run tests**

Run: `./test.sh`
Expected: `OK`. Existing server tests build `cfg := config.Defaults()` and then overwrite `cfg.Providers` — the `shell` entry disappears there, which is fine for those tests (Task 3 adds it back where needed).

- [ ] **Step 7: Commit**

`/usr/bin/git add -A internal/config internal/server/api_providers.go internal/server/config_live.go internal/server/config_reload_test.go internal/cli/provider.go` then commit: `feat(config): provider type (agent|tool), gg_bin, built-in shell tool provider + preset`.

---

### Task 2: internal/gg

**Files:**
- Create: `internal/gg/gg.go`, `internal/gg/gg_test.go`

**Interfaces:**
- Produces:
  - `type Runner func(dir, name string, args ...string) ([]byte, error)` (identical to `wt.Runner`, so `s.run` can be passed as is)
  - `func ExecRunner(dir, name string, args ...string) ([]byte, error)` — `exec.CommandContext` with a 60 s timeout, `cmd.Dir = dir`, `cmd.Stdin = nil`, `CombinedOutput()`; on error returns the output too.
  - `type Branch struct{ Name string; Current bool }`
  - `func Branches(run Runner, bin, root string) ([]Branch, error)` — `gg branch ls`
  - `type Worktree struct{ Branch, Path string }`
  - `func Worktrees(run Runner, bin, root string) ([]Worktree, error)` — `gg worktree list`
  - `type Created struct{ Branch, Path string }`
  - `func AddForBranch(run Runner, bin, root, branch string) (Created, error)` — `gg worktree add --branch <branch>`
  - `func AddFrom(run Runner, bin, root, base, name string) (Created, error)` — `gg worktree add --from <base> <name>`
  - `type Error struct{ Message string; Output string; Err error }` with `Error() string` returning `Message`.
  - `func Message(bin string, out []byte, err error) string` — the human line: `exec.ErrNotFound` → `gg not found (gg_bin = "<bin>"): install gg or set gg_bin in config.yaml`; else the last line starting with `error: ` (prefix stripped) ; else trimmed output ; else `err.Error()`.

- [ ] **Step 1: Write the failing tests**

```go
package gg

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Recorded from gg 2026-09-10 in a scratch repo (spec table).
const branchLs = "  feat/one\n  feat/one-2026-09-10_02-40\n* main ↑1 ↓2\n  feat/two\n"
const wtList = "main\t/r/ggrepo\nfeat/three\t/r/ggrepo.worktrees/feat-three\n"
const addOK = "→ creating worktree: feat/three → /r/ggrepo.worktrees/feat-three\n✓ created worktree feat/three at /r/ggrepo.worktrees/feat-three\n"
const addTaken = "error: create worktree: branch feat/three is already checked out in worktree /r/ggrepo.worktrees/feat-three\n"

type call struct {
	dir  string
	args []string
}

func script(t *testing.T, out string, err error) (Runner, *[]call) {
	t.Helper()
	var calls []call
	return func(dir, name string, args ...string) ([]byte, error) {
		if name != "gg" {
			t.Fatalf("ran %s, want gg", name)
		}
		calls = append(calls, call{dir, args})
		return []byte(out), err
	}, &calls
}

func TestBranches(t *testing.T) {
	run, calls := script(t, branchLs, nil)
	got, err := Branches(run, "gg", "/r/ggrepo")
	if err != nil {
		t.Fatal(err)
	}
	want := []Branch{{"feat/one", false}, {"feat/one-2026-09-10_02-40", false}, {"main", true}, {"feat/two", false}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("branch %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if c := (*calls)[0]; c.dir != "/r/ggrepo" || strings.Join(c.args, " ") != "branch ls" {
		t.Fatalf("call = %+v", c)
	}
}

func TestWorktrees(t *testing.T) {
	run, _ := script(t, wtList, nil)
	got, err := Worktrees(run, "gg", "/r/ggrepo")
	if err != nil || len(got) != 2 || got[1] != (Worktree{"feat/three", "/r/ggrepo.worktrees/feat-three"}) {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestAddFromParsesCreatedLine(t *testing.T) {
	run, calls := script(t, addOK, nil)
	c, err := AddFrom(run, "gg", "/r/ggrepo", "feat/two", "feat/three")
	if err != nil || c != (Created{"feat/three", "/r/ggrepo.worktrees/feat-three"}) {
		t.Fatalf("got %+v err %v", c, err)
	}
	if strings.Join((*calls)[0].args, " ") != "worktree add --from feat/two feat/three" {
		t.Fatalf("args = %v", (*calls)[0].args)
	}
}

func TestAddForBranchArgsAndError(t *testing.T) {
	run, calls := script(t, addTaken, errors.New("exit status 1"))
	_, err := AddForBranch(run, "gg", "/r/ggrepo", "feat/three")
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatalf("err = %T %v", err, err)
	}
	if ge.Message != "create worktree: branch feat/three is already checked out in worktree /r/ggrepo.worktrees/feat-three" {
		t.Fatalf("message = %q", ge.Message)
	}
	if strings.Join((*calls)[0].args, " ") != "worktree add --branch feat/three" {
		t.Fatalf("args = %v", (*calls)[0].args)
	}
}

func TestAddSuccessWithoutCreatedLine(t *testing.T) {
	run, _ := script(t, "something else\n", nil)
	_, err := AddForBranch(run, "gg", "/r", "b")
	if err == nil || !strings.Contains(err.Error(), "did not report the new worktree") {
		t.Fatalf("err = %v", err)
	}
}

func TestMessage(t *testing.T) {
	if m := Message("gg", nil, exec.ErrNotFound); m != `gg not found (gg_bin = "gg"): install gg or set gg_bin in config.yaml` {
		t.Fatal(m)
	}
	if m := Message("gg", []byte("noise\nerror: create worktree: no local branch \"x\"\n"), errors.New("exit status 1")); m != `create worktree: no local branch "x"` {
		t.Fatal(m)
	}
	if m := Message("gg", []byte("  plain failure  \n"), errors.New("exit status 1")); m != "plain failure" {
		t.Fatal(m)
	}
	if m := Message("gg", nil, errors.New("signal: killed")); m != "signal: killed" {
		t.Fatal(m)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/gg/`
Expected: package does not exist / undefined symbols.

- [ ] **Step 3: Implement**

`internal/gg/gg.go`:

```go
// Package gg runs gigagit (gg) for worktree creation on the happy path.
// Verified behaviour (2026-09-10, gg 26.x, no TTY, stdin closed) is in
// docs/superpowers/specs/2026-09-10-worktree-create-design.md:
//
//	gg branch ls                      "* main ↑1 ↓2" / "  feat/x" per line
//	gg worktree list                  "<branch>\t<path>" per line
//	gg worktree add --branch <b>      existing local branch → new worktree
//	gg worktree add --from <base> <n> new branch at base + worktree
//
// Success prints "✓ created worktree <branch> at <path>"; failures print
// "error: …" and exit 1. The bare "gg worktree add <x>" creates a dated
// NEW branch and is never used here.
package gg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Runner executes name with args in dir and returns the combined output.
// Same shape as wt.Runner so the server's runner serves both packages.
type Runner func(dir, name string, args ...string) ([]byte, error)

// Timeout bounds one gg invocation; gg never prompts without a TTY, so a
// long run means git itself is stuck.
const Timeout = 60 * time.Second

// ExecRunner runs the command with stdin closed and stderr merged.
func ExecRunner(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("gg timed out after %s", Timeout)
	}
	return out, err
}

type Branch struct {
	Name    string
	Current bool
}

type Worktree struct {
	Branch, Path string
}

type Created struct {
	Branch, Path string
}

// Error is a failed gg invocation: Message is the line for humans.
type Error struct {
	Message string
	Output  string
	Err     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// Message turns gg's output and exit error into one human line.
func Message(bin string, out []byte, err error) string {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Sprintf("gg not found (gg_bin = %q): install gg or set gg_bin in config.yaml", bin)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "error: ") {
			return strings.TrimPrefix(l, "error: ")
		}
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	if err != nil {
		return err.Error()
	}
	return "gg failed without output"
}

func exec1(run Runner, bin, root string, args ...string) ([]byte, error) {
	out, err := run(root, bin, args...)
	if err != nil {
		return out, &Error{Message: Message(bin, out, err), Output: string(out), Err: err}
	}
	return out, nil
}

// Branches lists local branches; Current marks HEAD's branch.
func Branches(run Runner, bin, root string) ([]Branch, error) {
	out, err := exec1(run, bin, root, "branch", "ls")
	if err != nil {
		return nil, err
	}
	var bs []Branch
	for _, l := range strings.Split(string(out), "\n") {
		cur := strings.HasPrefix(l, "* ")
		l = strings.TrimSpace(strings.TrimPrefix(l, "* "))
		if l == "" {
			continue
		}
		// "main ↑1 ↓2": the name is the first field.
		bs = append(bs, Branch{Name: strings.Fields(l)[0], Current: cur})
	}
	return bs, nil
}

// Worktrees lists checked-out worktrees (main first).
func Worktrees(run Runner, bin, root string) ([]Worktree, error) {
	out, err := exec1(run, bin, root, "worktree", "list")
	if err != nil {
		return nil, err
	}
	var ws []Worktree
	for _, l := range strings.Split(string(out), "\n") {
		br, path, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		ws = append(ws, Worktree{Branch: strings.TrimSpace(br), Path: strings.TrimSpace(path)})
	}
	return ws, nil
}

var createdRe = regexp.MustCompile(`created worktree (.+?) at (.+)$`)

func add(run Runner, bin, root string, args ...string) (Created, error) {
	out, err := exec1(run, bin, root, append([]string{"worktree", "add"}, args...)...)
	if err != nil {
		return Created{}, err
	}
	for _, l := range strings.Split(string(out), "\n") {
		if m := createdRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			return Created{Branch: m[1], Path: strings.TrimSpace(m[2])}, nil
		}
	}
	return Created{}, &Error{Message: "gg did not report the new worktree: " + strings.TrimSpace(string(out)), Output: string(out)}
}

// AddForBranch checks an existing local branch out into a new worktree.
func AddForBranch(run Runner, bin, root, branch string) (Created, error) {
	return add(run, bin, root, "--branch", branch)
}

// AddFrom creates branch name at base and a worktree on it, in one step.
func AddFrom(run Runner, bin, root, base, name string) (Created, error) {
	return add(run, bin, root, "--from", base, name)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gg/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

`/usr/bin/git add internal/gg` → `feat(gg): wrapper for gg branch ls / worktree list / worktree add (--branch, --from)`.

---

### Task 3: Tool runs

**Files:**
- Modify: `internal/server/runs.go` (spawnRunCore steps 6–7 and 11, ~263-352)
- Modify: `internal/server/watch.go:48-56` (loop), `internal/server/ui_channel.go` (`runView`, `toView`, `sendToRun`), `internal/server/ui_forward.go:40-56` (targets), `internal/web/templates/channel.html:41`, `internal/web/templates/_runs.html`, `internal/web/static/app.css`
- Create: `internal/server/tool_run_test.go`

**Interfaces:**
- Consumes: `config.Provider.IsTool()`, `config.ShellProvider()`, `config.ShellPreset()` (Task 1).
- Produces: `runView.Tool bool`; `func (s *Server) isToolRun(run store.AgentRun) bool` (looks the provider up via `s.providerCfg(run.Provider)` and returns `p.IsTool()`; unknown provider → false); `sendToRun` returns `"<agent> is a shell, not an agent — nothing was typed"` for tool runs.

- [ ] **Step 1: Write the failing tests**

`internal/server/tool_run_test.go`:

```go
package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
)

// withShell adds the built-in shell provider/preset to the test server
// (newTestServer replaces cfg.Providers wholesale).
func withShell(t *testing.T) {
	t.Helper()
	testSrv.rulesMu.Lock()
	testSrv.cfg.Providers["shell"] = config.ShellProvider()
	testSrv.cfg.Presets["shell"] = config.ShellPreset()
	testSrv.rulesMu.Unlock()
}

func TestToolRunSpawnsBareShell(t *testing.T) {
	ts, st, root := newTestServer(t)
	withShell(t)
	fs := &fakeSpawner{handle: "s:9"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)

	resp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "preset": "shell", "prompt": "ignored"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spawn: %d", resp.StatusCode)
	}
	rj := decode[map[string]any](t, resp)
	id := int64(rj["id"].(float64))
	run, _, _ := st.RunByID(id)
	if run.Provider != "shell" || run.Prompt != "" || run.Workdir != root {
		t.Fatalf("run = %+v", run)
	}
	if len(fs.specs) != 1 || fs.specs[0].WindowName != filepathBase(root)+"/shell" {
		t.Fatalf("specs = %+v", fs.specs)
	}
	// cmd.sh holds the bare shell: no preamble, no hook args, no prompt.
	cmd := readCmdSh(t, testSrv.dataDir, id)
	if !strings.Contains(cmd, "exec ${SHELL:-bash}") || strings.Contains(cmd, "erbrus msg") || strings.Contains(cmd, "ignored") {
		t.Fatalf("cmd.sh = %q", cmd)
	}
	if len(fs.sent) != 0 {
		t.Fatalf("pasted into a shell: %v", fs.sent)
	}
}

func TestToolRunIsNotClassifiedAndNotATarget(t *testing.T) {
	ts, st, root := newTestServer(t)
	withShell(t)
	fs := &fakeSpawner{handle: "s:9", alive: map[spawn.Handle]bool{"s:9": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	resp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "preset": "shell"})
	id := int64(decode[map[string]any](t, resp)["id"].(float64))

	// A screen that any agent rule set would call "working" changes nothing.
	fs.setScreen("s:9", spawn.Screen{Raw: "Working (3s · esc to interrupt)\n", Activity: time.Now().Add(-10 * time.Minute)})
	if err := testSrv.WatchScreens(); err != nil {
		t.Fatal(err)
	}
	if _, ok := testSrv.stateOf(id); ok {
		t.Fatal("tool run was classified")
	}

	page := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if strings.Contains(page, fmt.Sprintf(`<option value="r%d">`, id)) {
		t.Fatal("shell offered as composer target")
	}
	if !strings.Contains(page, `class="badge tool"`) {
		t.Fatal("no shell badge on the run card")
	}
	fwd := getBody(t, ts.URL+"/ui/forward?message="+firstMessageID(t, st, ch1))
	if strings.Contains(fwd, fmt.Sprintf(`value="r%d"`, id)) {
		t.Fatal("shell offered as forward target")
	}

	run, _, _ := st.RunByID(id)
	if w, queued := testSrv.sendToRun(run, "hello"); queued || !strings.Contains(w, "is a shell, not an agent") {
		t.Fatalf("sendToRun = %q %v", w, queued)
	}
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"target": {fmt.Sprintf("r%d", id)}, "body": {"hi"}})
	r.Body.Close()
	if !strings.Contains(r.Header.Get("Location"), "warning=") || len(fs.sent) != 0 {
		t.Fatalf("composer typed into the shell: loc=%s sent=%v", r.Header.Get("Location"), fs.sent)
	}
}
```

Helpers: check `server_test.go`/other tests for existing `getBody`, `readCmdSh`, `firstMessageID`, `filepathBase`. Add the missing ones to `tool_run_test.go`:

```go
func filepathBase(p string) string { return filepath.Base(p) }

func readCmdSh(t *testing.T, dataDir string, id int64) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "runs", fmt.Sprint(id), "cmd.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func getBody(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// firstMessageID posts a memo (the system "spawned" note is not
// forwardable in the dialog's terms — use a real message).
func firstMessageID(t *testing.T, st *store.Store, ch int64) string {
	t.Helper()
	m, err := st.CreateMessage(store.Message{ChannelID: ch, Kind: "memo", AuthorKind: "human", AuthorName: "h", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(m.ID)
}
```

(add `io`, `os`, `path/filepath`, `erbrus/internal/store` imports; drop any helper that already exists elsewhere in the package).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run ToolRun -v`
Expected: FAIL — cmd.sh contains the preamble, the run is classified, targets listed, `sendToRun` types.

- [ ] **Step 3: spawnRunCore tool branch**

In `runs.go` after `providerName` is validated (after the `unknown provider` check) add:

```go
	tool := providers[providerName].IsTool()
	if tool {
		// A tool is a terminal program for the human: nothing to prompt.
		promptText = ""
	}
```

(move the `promptText := firstNonEmpty(...)` line above this or convert to assignment accordingly.)

Step 6 (hook args): wrap — `hookArgs := ""` then `if !tool { hookArgs, _ = integrate.ProviderHook(providerName, runDir, s.erbrusBin) }`.

Step 7: replace the preamble/paste block with:

```go
	fullPrompt, paste := "", false
	if !tool {
		preamble := integrate.Preamble(s.erbrusBin, agentName, channel.Name)
		fullPrompt = integrate.AssemblePrompt(preamble, promptText, handoffContext)
		paste = pasteMode(providers[providerName])
	}
	cmdPrompt := fullPrompt
	if paste {
		cmdPrompt = ""
	}
```

The rest (cmd.sh, env, spawn, `if paste { go s.deliverPrompt }`) stays: with `paste == false` and an empty prompt, `Render` drops `{prompt}` and cmd.sh is `exec ${SHELL:-bash}`.

- [ ] **Step 4: Watcher, views, targets, delivery**

`watch.go` loop: after the `Spawner != "tmux"` guard add `if s.isToolRun(r) { continue }` (the run is also excluded from `live`, so no stale state lingers).

`ui_channel.go`: add to `runView` — `Tool bool // provider type "tool": no state badge, not a chat target`; in `toView` set `Tool: s.isToolRun(r)` and wrap the `if v.Running { if rs, ok := s.stateOf(...)` block in `if v.Running && !v.Tool`. Add:

```go
// isToolRun: the run's provider is a tool (shell), keyed on Provider.Type.
func (s *Server) isToolRun(r store.AgentRun) bool {
	p, ok := s.providerCfg(r.Provider)
	return ok && p.IsTool()
}
```

`sendToRun`: first check → `if s.isToolRun(run) { return fmt.Sprintf("%s is a shell, not an agent — nothing was typed", run.AgentName), false }`.

`channel.html:41`: `{{range .Runs}}{{if and .Running (not .Tool)}}<option …`.

`ui_forward.go` `forwardTargets`: inside the `running` loop add `if s.isToolRun(r) { continue }` before the channel lookup.

`_runs.html`: on the running branch replace the state span line with

```
{{if .Tool}}<span class="badge tool">shell</span>{{else if .StateLabel}}<span class="{{.StateClass}}"…>{{.StateLabel}}</span>{{else}}<span class="state">detecting…</span>{{end}}
```

(keep the existing `data-since`/`data-label` attributes on the StateLabel span exactly as they are).

`app.css`: `.badge.tool { background: #3a3f47; color: #d6d8dc; }` next to the other `.badge` rules (search `.badge.warn`).

- [ ] **Step 5: Run tests**

Run: `./test.sh`
Expected: `OK`.

- [ ] **Step 6: Commit**

`feat(runs): tool-type providers spawn a bare terminal — no preamble/prompt/paste, no classification, not a chat target`.

---

### Task 4: Worktree form, gg execution, shell terminal button

**Files:**
- Create: `internal/server/ui_worktree.go`, `internal/server/ui_worktree_test.go`, `internal/web/templates/worktree.html`
- Modify: `internal/server/server.go:170-175` (routes), `internal/web/templates/channel.html:17` (sidebar), `internal/web/static/app.css`

**Interfaces:**
- Consumes: `gg.Branches/Worktrees/AddForBranch/AddFrom` with `s.run` and `s.ggBin()` (Tasks 1–2); `s.syncChannels(p)`; `s.spawnRunCore(runRequest{ChannelID, Preset: "shell", Workdir: p.RepoPath})` (Task 3); `store.ChannelsByProject`.
- Produces routes: `GET /ui/projects/{id}/worktree`, `POST /ui/projects/{id}/worktree` (form: `mode` = `existing`|`new`, `branch`, `name`, `base`), `POST /ui/projects/{id}/shell`.
- Page data:

```go
type worktreePage struct {
	Project  store.Project
	MainID   int64          // the project's main channel (Cancel link, shell button)
	Branches []branchOption // local branches
	Current  string         // HEAD branch of the main worktree (default base)
	Mode     string         // "existing"|"new" (echo; default "new")
	Form     map[string]string
	Error    string         // gg/validation message; ListError when the lists failed
	ListError bool          // branch listing failed: form hidden, terminal button shown
}
type branchOption struct {
	Name       string
	CheckedOut string // directory basename when checked out somewhere, else ""
}
```

- [ ] **Step 1: Write the failing tests**

`internal/server/ui_worktree_test.go`:

```go
package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
)

// ggScript routes gg calls to a fake; everything else keeps the fixture
// runner (wt). Returns the recorded gg args.
func ggScript(t *testing.T, root string, replies map[string]struct {
	out string
	err error
}) *[]string {
	t.Helper()
	var calls []string
	prev := testSrv.run
	testSrv.run = func(dir, name string, args ...string) ([]byte, error) {
		if name != "gg" {
			return prev(dir, name, args...)
		}
		if dir != root {
			t.Errorf("gg ran in %s, want %s", dir, root)
		}
		key := strings.Join(args, " ")
		calls = append(calls, key)
		r, ok := replies[key]
		if !ok {
			t.Errorf("unexpected gg %s", key)
			return nil, errors.New("unexpected")
		}
		return []byte(r.out), r.err
	}
	t.Cleanup(func() { testSrv.run = prev })
	return &calls
}

type reply = struct {
	out string
	err error
}

func projectID(t *testing.T, tsURL, root string) (pid, mainCh int64) {
	t.Helper()
	resp := postJSON(t, tsURL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	pid = int64(p["id"].(float64))
	mainCh = int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	return
}

func TestWorktreeFormListsBranches(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, mainCh := projectID(t, ts.URL, root)
	ggScript(t, root, map[string]reply{
		"branch ls":     {out: "* main\n  feat/x\n  wt/feat ↑1\n"},
		"worktree list": {out: fmt.Sprintf("main\t%s\nwt/feat\t%s-wt-feat\n", root, root)},
	})
	body := getBody(t, fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid))
	for _, want := range []string{
		`<option value="feat/x">feat/x</option>`,
		fmt.Sprintf(`<option value="wt/feat" disabled>wt/feat (checked out in %s)</option>`, filepath.Base(root+"-wt-feat")),
		fmt.Sprintf(`<option value="main" disabled>main (checked out in %s)</option>`, filepath.Base(root)),
		`<option value="main" selected>main</option>`, // base select defaults to HEAD
		fmt.Sprintf(`href="/ui/channels/%d"`, mainCh),
		`name="mode" value="new" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

func TestWorktreeFormWhenGgMissing(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	ggScript(t, root, map[string]reply{"branch ls": {err: execNotFound()}})
	body := getBody(t, fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid))
	if !strings.Contains(body, `gg not found (gg_bin = "gg")`) || strings.Contains(body, `name="mode"`) {
		t.Fatalf("expected the error without the form:\n%s", body)
	}
	if !strings.Contains(body, fmt.Sprintf(`action="/ui/projects/%d/shell"`, pid)) {
		t.Fatal("terminal button missing")
	}
}

func TestWorktreeCreateNewBranchRedirectsToChannel(t *testing.T) {
	ts, st, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	newPath := root + "-wt-hot"
	if err := os.MkdirAll(newPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// After creation the wt fixture must list the new worktree too.
	swapWt(t, fmt.Sprintf(`[{"path":%q,"branch":"refs/heads/main","head":"a","is_main":true},{"path":%q,"branch":"refs/heads/wt/feat","head":"b","is_main":false},{"path":%q,"branch":"refs/heads/hot","head":"c","is_main":false}]`, root, root+"-wt-feat", newPath))
	calls := ggScript(t, root, map[string]reply{
		"worktree add --from main hot": {out: "→ creating worktree: hot → " + newPath + "\n✓ created worktree hot at " + newPath + "\n"},
	})
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid),
		url.Values{"mode": {"new"}, "name": {"hot"}, "base": {"main"}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if len(*calls) != 1 {
		t.Fatalf("gg calls = %v", *calls)
	}
	chans, _ := st.ChannelsByProject(pid)
	var hot int64
	for _, c := range chans {
		if c.WorktreePath == newPath {
			hot = c.ID
		}
	}
	if hot == 0 {
		t.Fatalf("no channel for %s: %+v", newPath, chans)
	}
	if loc := r.Header.Get("Location"); r.StatusCode != http.StatusFound || loc != fmt.Sprintf("/ui/channels/%d", hot) {
		t.Fatalf("redirect = %d %s", r.StatusCode, loc)
	}
}

func TestWorktreeCreateExistingBranchUsesBranchFlag(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	calls := ggScript(t, root, map[string]reply{
		"worktree add --branch feat/x": {out: "error: create worktree: no local branch \"feat/x\"\n", err: errors.New("exit status 1")},
		"branch ls":                    {out: "* main\n"},
		"worktree list":                {out: fmt.Sprintf("main\t%s\n", root)},
	})
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid),
		url.Values{"mode": {"existing"}, "branch": {"feat/x"}})
	defer r.Body.Close()
	body := readAll(r)
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", r.StatusCode)
	}
	if (*calls)[0] != "worktree add --branch feat/x" {
		t.Fatalf("calls = %v", *calls)
	}
	for _, want := range []string{`no local branch &#34;feat/x&#34;`, fmt.Sprintf(`action="/ui/projects/%d/shell"`, pid), `name="mode" value="existing" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("failure page missing %q", want)
		}
	}
}

func TestWorktreeValidation(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	calls := ggScript(t, root, map[string]reply{"branch ls": {out: "* main\n"}, "worktree list": {out: "main\t" + root + "\n"}})
	for _, f := range []url.Values{
		{"mode": {"new"}, "name": {""}, "base": {"main"}},
		{"mode": {"new"}, "name": {"has space"}, "base": {"main"}},
		{"mode": {"new"}, "name": {"x"}, "base": {""}},
		{"mode": {"existing"}, "branch": {""}},
		{"mode": {"weird"}},
	} {
		r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid), f)
		r.Body.Close()
		if r.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v: status %d", f, r.StatusCode)
		}
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "worktree add") {
			t.Fatalf("gg add ran on invalid input: %v", *calls)
		}
	}
}

func TestShellButtonSpawnsShellInMainChannel(t *testing.T) {
	ts, st, root := newTestServer(t)
	withShell(t)
	fs := &fakeSpawner{handle: "s:7"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	pid, mainCh := projectID(t, ts.URL, root)
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/shell", ts.URL, pid), nil)
	r.Body.Close()
	runs, _ := st.RunsByChannel(mainCh)
	if len(runs) != 1 || runs[0].Provider != "shell" || runs[0].Workdir != root {
		t.Fatalf("runs = %+v", runs)
	}
	if loc := r.Header.Get("Location"); loc != fmt.Sprintf("/ui/runs/%d/terminal", runs[0].ID) {
		t.Fatalf("redirect = %s", loc)
	}
	_ = spawn.Handle("") // keep import if unused elsewhere
	_ = config.ShellPreset
}
```

Helpers to add in this file unless they already exist (grep first): `execNotFound()` returns `&exec.Error{Name: "gg", Err: exec.ErrNotFound}` (import `os/exec`); `readAll(r *http.Response) string`; `swapWt(t, json string)` replaces `testSrv.run` so `name == "wt"` returns the given JSON (chain to the previous runner otherwise, restore in Cleanup — note `ggScript` must be installed AFTER `swapWt` so both chain). Check `store` for the runs-by-channel method name (`RunsByChannel` or similar, see `handleChannelRuns`) and use that.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run 'Worktree|ShellButton' -v`
Expected: 404s / compile errors.

- [ ] **Step 3: Template**

`internal/web/templates/worktree.html`:

```html
{{define "title"}}new worktree · erbrus{{end}}
{{define "content"}}
<header class="topbar">
  <a class="brand" href="/ui/projects">erbrus</a>
  <span class="crumb">/ {{.Project.Name}} / new worktree</span>
  <span class="spacer"></span>
</header>
<main class="page">
  <div class="dialog">
    <h1>New worktree for {{.Project.Name}}</h1>
    <div class="muted small mono">{{.Project.RepoPath}}</div>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    {{if or .Error .ListError}}
    <form method="post" action="/ui/projects/{{.Project.ID}}/shell" class="inlineform">
      <button class="btn" type="submit" title="opens a shell in the main repository in the web terminal">Open a terminal in {{.Project.RepoPath}}</button>
    </form>
    {{end}}
    {{if not .ListError}}
    <form method="post" action="/ui/projects/{{.Project.ID}}/worktree" id="wtform">
      <label class="toggle"><input type="radio" name="mode" value="new"{{if eq .Mode "new"}} checked{{end}}> New branch</label>
      <label class="toggle"><input type="radio" name="mode" value="existing"{{if eq .Mode "existing"}} checked{{end}}> Existing branch</label>

      <div data-mode="new">
        <label>Branch name</label>
        <input type="text" name="name" value="{{index .Form "name"}}" placeholder="feat/thing">
        <label>Base branch</label>
        <select name="base">
          {{range .Branches}}<option value="{{.Name}}"{{if eq .Name $.Current}} selected{{end}}>{{.Name}}</option>{{end}}
        </select>
      </div>
      <div data-mode="existing">
        <label>Branch</label>
        <select name="branch">
          {{range .Branches}}{{if .CheckedOut}}<option value="{{.Name}}" disabled>{{.Name}} (checked out in {{.CheckedOut}})</option>{{else}}<option value="{{.Name}}"{{if eq .Name (index $.Form "branch")}} selected{{end}}>{{.Name}}</option>{{end}}{{end}}
        </select>
      </div>
      <div class="muted small">gg decides where the worktree goes (its <span class="mono">[worktree] path_template</span>); the new channel takes the branch's short name.</div>

      <div class="foot">
        <a class="btn" href="/ui/channels/{{.MainID}}">Cancel</a>
        <button class="btn primary" type="submit">Create worktree</button>
      </div>
    </form>
    {{end}}
  </div>
</main>
{{end}}
```

Note on the base select: the test expects `<option value="main" selected>main</option>` — the `{{if eq .Name $.Current}} selected{{end}}` renders exactly that. If the form is re-rendered after an error with `Form["base"]` set, prefer that: use `{{if eq .Name (or (index $.Form "base") $.Current)}}` — `or` returns the first non-empty string in Go templates.

Add to `app.js` (a small IIFE, same style as the rail one): on `#wtform`, show only the `[data-mode]` block matching the checked radio, re-run on radio change. Mode blocks are plain `<div>` toggled with `hidden`.

- [ ] **Step 4: Handlers**

`internal/server/ui_worktree.go`:

```go
package server

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"erbrus/internal/gg"
	"erbrus/internal/store"
)

type branchOption struct {
	Name       string
	CheckedOut string // directory basename when checked out somewhere
}

type worktreePage struct {
	Project   store.Project
	MainID    int64
	Branches  []branchOption
	Current   string
	Mode      string
	Form      map[string]string
	Error     string
	ListError bool
}

// branchNameRe: what git accepts in practice for our purposes — no
// whitespace, no leading dash, no "..".
var branchNameRe = regexp.MustCompile(`^[^\s\-][^\s]*$`)

func (s *Server) projectAndMain(id int64) (store.Project, int64, int, string) {
	p, ok, err := s.st.ProjectByID(id)
	if err != nil {
		return p, 0, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return p, 0, http.StatusNotFound, "project not found"
	}
	chans, err := s.st.ChannelsByProject(p.ID)
	if err != nil {
		return p, 0, http.StatusInternalServerError, err.Error()
	}
	for _, c := range chans {
		if c.WorktreePath == "" || c.WorktreePath == p.RepoPath {
			return p, c.ID, 0, ""
		}
	}
	return p, 0, http.StatusNotFound, "project has no main channel"
}

// buildWorktreePage lists branches through gg; a listing failure becomes
// the page's error with the form hidden (ListError).
func (s *Server) buildWorktreePage(p store.Project, mainID int64) worktreePage {
	page := worktreePage{Project: p, MainID: mainID, Mode: "new", Form: map[string]string{}}
	bin := s.ggBin()
	branches, err := gg.Branches(s.run, bin, p.RepoPath)
	if err != nil {
		page.Error, page.ListError = err.Error(), true
		return page
	}
	wts, err := gg.Worktrees(s.run, bin, p.RepoPath)
	if err != nil {
		page.Error, page.ListError = err.Error(), true
		return page
	}
	where := map[string]string{}
	for _, w := range wts {
		where[w.Branch] = filepath.Base(w.Path)
	}
	for _, b := range branches {
		page.Branches = append(page.Branches, branchOption{Name: b.Name, CheckedOut: where[b.Name]})
		if b.Current {
			page.Current = b.Name
		}
	}
	return page
}

func (s *Server) handleUIWorktree(w http.ResponseWriter, r *http.Request) {
	p, mainID, status, errMsg := s.projectAndMain(chiInt64(r, "id"))
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	s.render(w, "worktree", s.buildWorktreePage(p, mainID))
}

func (s *Server) handleUIWorktreePost(w http.ResponseWriter, r *http.Request) {
	p, mainID, status, errMsg := s.projectAndMain(chiInt64(r, "id"))
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	mode := r.FormValue("mode")
	form := map[string]string{"name": r.FormValue("name"), "base": r.FormValue("base"), "branch": r.FormValue("branch")}
	fail := func(msg string) {
		page := s.buildWorktreePage(p, mainID)
		page.Mode, page.Form, page.Error = mode, form, msg
		if page.ListError { // the listing error is less useful than the real one
			page.Error = msg
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.renderStatus(w, "worktree", page)
	}
	var created gg.Created
	var err error
	bin := s.ggBin()
	switch mode {
	case "new":
		name, base := strings.TrimSpace(form["name"]), strings.TrimSpace(form["base"])
		if !branchNameRe.MatchString(name) || strings.Contains(name, "..") {
			fail("branch name is required and may not contain whitespace")
			return
		}
		if base == "" {
			fail("base branch is required")
			return
		}
		created, err = gg.AddFrom(s.run, bin, p.RepoPath, base, name)
	case "existing":
		branch := strings.TrimSpace(form["branch"])
		if branch == "" {
			fail("pick a branch")
			return
		}
		created, err = gg.AddForBranch(s.run, bin, p.RepoPath, branch)
	default:
		fail("mode must be new or existing")
		return
	}
	if err != nil {
		fail(err.Error())
		return
	}
	summary, warning := s.syncChannels(p)
	_ = summary
	chans, lerr := s.st.ChannelsByProject(p.ID)
	if lerr == nil {
		for _, c := range chans {
			if c.WorktreePath == created.Path {
				http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", c.ID), http.StatusFound)
				return
			}
		}
	}
	note := fmt.Sprintf("worktree created at %s, but no channel appeared for it", created.Path)
	if warning != "" {
		note += ": " + warning
	}
	http.Redirect(w, r, "/ui/projects?warning="+url.QueryEscape(note), http.StatusFound)
}

// handleUIProjectShell opens a shell in the main repository: a run of the
// built-in "shell" tool provider in the project's main channel, then the
// web terminal for it.
func (s *Server) handleUIProjectShell(w http.ResponseWriter, r *http.Request) {
	p, mainID, status, errMsg := s.projectAndMain(chiInt64(r, "id"))
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	payload, status, errMsg := s.spawnRunCore(runRequest{ChannelID: mainID, Preset: "shell", Workdir: p.RepoPath})
	if status != 0 {
		http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d?warning=%s", mainID, url.QueryEscape("shell: "+errMsg)), http.StatusFound)
		return
	}
	rj, _ := payload.(runJSON)
	http.Redirect(w, r, fmt.Sprintf("/ui/runs/%d/terminal", rj.ID), http.StatusFound)
}
```

Check `s.render`'s signature: if it always writes 200, add a `renderStatus(w, name, data)` variant that skips `WriteHeader` (or look at how `renderForwardError` writes 422 — it executes the template directly; mirror that exact pattern instead of a new helper). Check the type name `spawnRunCore` returns on the tmux path (`rj := toRunJSON(run)` — find `toRunJSON`'s return type and use it in the assertion).

Routes in `server.go` after the `sync` route:

```go
	r.Get("/ui/projects/{id}/worktree", s.handleUIWorktree)
	r.Post("/ui/projects/{id}/worktree", s.handleUIWorktreePost)
	r.Post("/ui/projects/{id}/shell", s.handleUIProjectShell)
```

Sidebar `channel.html:17`:

```html
    <div class="proj">{{.Project.Name}} <a class="plus" href="/ui/projects/{{.Project.ID}}/worktree" title="new worktree for {{.Project.Name}}">+</a></div>
```

`app.css`: `.sidebar .proj .plus { float: right; color: #a7acb5; padding: 0 4px; } .sidebar .proj .plus:hover { color: #e8eaee; }`.

- [ ] **Step 5: Run tests**

Run: `./test.sh`
Expected: `OK`. Fix template escaping expectations in the tests against the real output rather than loosening the handler.

- [ ] **Step 6: Commit**

`feat(ui): "+" per project creates a worktree via gg (existing branch or new branch from base); failure offers a shell terminal in the repo`.

---

### Task 5: Docs, backlog, live check

**Files:**
- Modify: `docs/superpowers/specs/2026-09-10-worktree-create-design.md` (amendments section), `docs/BACKLOG.md`

- [ ] **Step 1: Spec amendments** — append an "Amendments (implementation)" section: (a) `shell` is a built-in default provider + preset merged in `LoadGlobal`, not an agents-registry entry; `agents setup` is not involved; (b) channels are named by `wt.ShortBranch`, the existing rule; (c) any deviation discovered in Tasks 1–4.
- [ ] **Step 2: Backlog** — add under verification: "worktree '+' on the user's server with the real gg; `gg_bin` when gg is only a shell function (the server needs the binary on PATH)"; under ideas: "worktree removal from the channel page via `gg worktree remove`", "`agents list` type column".
- [ ] **Step 3: Live check** on the throwaway instance. Write `scratchpad/wtlive.sh`: create a scratch git repo with two branches under the scratchpad, start 7499 from the worktree's freshly built binary (`go build -o bin/erbrus ./cmd/erbrus` in the worktree; point `start7499.sh`'s BIN at it), `ERBRUS_URL=http://127.0.0.1:7499 bin/erbrus add-project <scratch repo>` (or the equivalent API POST), then with python `urllib`: GET the form (expect both branches), POST mode=new name=feat/live base=main (expect 302 to a channel whose path is `<repo>.worktrees/feat-live`), POST mode=existing branch=feat/live (expect 422 with "already checked out"), POST `/shell` (expect 302 to `/ui/runs/N/terminal`; then `tmux capture-pane` on the `claudetest-*` window shows a shell prompt). Stop the run through the API, kill the 7499 instance and the `claudetest-*` session. Record the outcome in the final report.
- [ ] **Step 4: Commit** — `docs: spec amendments + backlog for worktree creation`.
- [ ] **Step 5: Hand-over** — run `./test.sh` once more, build the binary in the worktree, report the absolute binary path and the commit list. Do NOT merge; the user decides.

---

## Self-review notes

- Spec coverage: config (Task 1), gg package (2), tool runs incl. watcher/targets/refusal/badge (3), form + outcomes + terminal button + sidebar (4), docs + live (5). `agents list` type column dropped with the registry decision (amendment, backlog idea).
- Type consistency: `gg.Runner` == `wt.Runner` shape so `s.run` passes to both; `runView.Tool`; `isToolRun`; `worktreePage`/`branchOption`; routes match the templates' `action`s and the tests' URLs.
- Placeholders: none; every code step is complete. Helper names in tests must be reconciled with existing helpers before adding duplicates (`grep -n 'func getBody\|func readAll\|func swapWt' internal/server/*_test.go`).
