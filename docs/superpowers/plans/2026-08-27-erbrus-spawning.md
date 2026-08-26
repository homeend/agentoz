# erbrus Plan 2/3 — Agent Spawning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `erbrus start <preset>` (and the API behind it) spawns a real provider CLI into tmux — or the current terminal with `--fg` — wired so the agent knows how to report to its channel, its exit is always recorded, and a report can seed the next agent's prompt.

**Architecture:** New `internal/spawn` (Spawner interface + TmuxSpawner over an exec seam) and `internal/integrate` (prompt preamble, Claude Code stop-hook settings, Codex notify). The server gains a runs API (`POST /api/runs`, exit/stop endpoints, run SSE events) and reconciles orphaned runs at startup. The CLI gains `start` and the hidden `wrap` mode. Also lands the Plan-1 carry-overs: per-repo config relocation, project-name collision handling, store-error 500s, and re-detection of worktrees on existing projects.

**Tech Stack:** Go 1.26, existing deps only — **zero new Go dependencies** (tmux is exec'd, not linked). tmux ≥ 3.0 required at runtime for `new-window -e` (3.7b installed).

**Spec:** /mnt/t/others/erbrus/docs/superpowers/specs/2026-08-26-erbrus-design.md
**Predecessor:** /mnt/t/others/erbrus/docs/superpowers/plans/2026-08-26-erbrus-core-server.md (executed on branch `plan1-core`, head 2434d9c) — its "Post-execution carry-overs" section is folded into Tasks 1–2 here.

## Global Constraints

Standing user rules — these override any default habit:

1. Always develop on a git worktree. **Execution starts by creating a worktree branched FROM `plan1-core`** (Plan 1 is unmerged; branching from main will not compile): `git worktree add -b plan2-spawn <path> plan1-core`. All paths below are relative to that worktree root.
2. After finishing this plan, build and provide the binary's **absolute path** for the user's manual testing.
3. Do not merge to main (or into plan1-core); the user merges.
4. Never ask about pushing to remote, and never push.
5. Always reference files with absolute paths when talking to the user.

Pinned decisions (spec's "plan picks one" items — implementers do not revisit):

- **Wrap transport:** the rendered provider command is written to `<dataDir>/runs/<runID>/cmd.sh` (`#!/bin/sh` + `exec <command>`); the tmux window runs `<erbrusBin> wrap <cmdfile>`. No command text ever travels through an env var (env size limits; quoting).
- **Run env** (`ERBRUS_URL`, `ERBRUS_TOKEN`, `ERBRUS_CHANNEL`, `ERBRUS_RUN_ID`) is injected once, via `tmux new-window -e KEY=VAL` flags (sorted by key for deterministic tests); `wrap` and the provider CLI both inherit it. The `--fg` path sets the same env on the in-process command.
- **Server wiring:** `server.New(st, cfg, run)` keeps its Plan-1 signature (every existing test compiles untouched). A new method `(*Server) SetRuntime(sp spawn.Spawner, erbrusBin, baseURL string)` is called by `serve` (and by tests that need spawning). `POST /api/runs` with `fg=false` returns 503 `"spawning unavailable"` when no spawner is set; `fg=true` needs no spawner.
- **erbrusBin** is `os.Executable()` resolved in `serve` — never a bare `erbrus` (during development the binary lives in a worktree `bin/`, not on PATH). The preamble, the stop hook, and the notify script all use this absolute path.
- **Hook wiring is keyed by provider name:** `"claude-code"` gets a generated settings JSON with a Stop hook (passed via `--settings`); `"codex"` gets a notify script. **Verify the exact codex `notify` config syntax against the installed `codex` binary during implementation** (`codex --help` / config docs); if it cannot be verified on this machine, wire preamble + wrapper only for codex and say so in the task report. All other providers: preamble + exit wrapper only (spec-sanctioned).
- **Reconcile touches tmux-spawned runs only.** `fg` runs cannot be verified from the server; their wrapper may still report — leave them running.
- **`remain-on-exit on`** is set per spawned window so output survives agent exit; dead windows accumulate until the user closes them or `Stop` kills them. Accepted v1 behavior.
- **Handoff context** (spawn-from-report) appends the report body, each artifact's **absolute path** (never inlined file content — everything is local; the agent reads files itself), and a provenance line to the prompt.
- Per-repo config path (user request): `~/.erbrus/repos/<encoded-repo-path>/.erbrus.yaml`, encoding = absolute repo path with every `/` replaced by `-`, keeping the leading dash (`/home/homeend/git-focus` → `-home-homeend-git-focus`). Global config STAYS at `~/.config/erbrus/config.yaml`. Legacy `<repo>/.erbrus.yaml` still read as fallback with a deprecation note.

Process constraints:

- TDD throughout: failing test first, watch it fail for the right reason, implement, watch it pass, commit.
- Verify each import in this plan's code blocks is actually used before compiling; drop unused ones (recurring Plan-1 paper-cut).
- Agent-facing instruction text must note stdlib-flag ordering: flags before positionals (`erbrus msg send --report "text"`).

---

### Task 1: Per-repo config relocation (internal/config + init)

**Files:**
- Modify: `internal/config/config.go` (add `EncodeRepoPath`, `RepoConfigPath`, rework `LoadRepo`)
- Modify: `internal/cli/initcmd.go` (scaffold at the new location)
- Test: `internal/config/config_test.go`, `internal/cli/serve_test.go` (adjust init test)

**Interfaces:**
- Consumes: existing `config.Repo`, `ExpandHome`.
- Produces:

```go
// EncodeRepoPath maps an absolute repo path to its config-dir name:
// every "/" becomes "-", keeping the leading dash.
func EncodeRepoPath(repoRoot string) string            // "/a/b" -> "-a-b"
// RepoConfigPath returns ~/.erbrus/repos/<encoded>/.erbrus.yaml (honoring
// ERBRUS_HOME env override for tests; default is $HOME/.erbrus).
func RepoConfigPath(repoRoot string) string
// LoadRepo now reads RepoConfigPath(repoRoot) first; if absent, falls back
// to the legacy <repoRoot>/.erbrus.yaml and returns legacy=true so callers
// can print a deprecation note. Missing both => zero Repo, legacy=false.
func LoadRepo(repoRoot string) (Repo, bool, error)     // (repo, legacy, err) — SIGNATURE CHANGE
```

The `LoadRepo` signature change ripples to exactly one Plan-1 caller: `internal/server/projects.go` (`repoCfg, _ := config.LoadRepo(root)` → `repoCfg, _, _ := ...`). Update it in this task so the tree compiles.

- [ ] **Step 1: Write the failing tests** (append to `internal/config/config_test.go`)

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL — undefined `EncodeRepoPath` / `RepoConfigPath`, wrong LoadRepo arity

- [ ] **Step 3: Implement**

In `internal/config/config.go`:

```go
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
```

Update `internal/server/projects.go`: `repoCfg, _, _ := config.LoadRepo(root)` (warning-surfacing for legacy configs on the server side is not required in this task).

Update `internal/cli/initcmd.go` — scaffold goes to the new location and announces it; a legacy file triggers a deprecation note:

```go
	scaffold := config.RepoConfigPath(root)
	if _, err := os.Stat(scaffold); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(scaffold), 0o755); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := os.WriteFile(scaffold, []byte(scaffoldYAML), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "scaffolded %s\n", scaffold)
	}
	if _, err := os.Stat(filepath.Join(root, ".erbrus.yaml")); err == nil {
		fmt.Fprintf(stdout, "note: legacy %s/.erbrus.yaml still read as fallback; move settings to %s\n", root, scaffold)
	}
```

(import `erbrus/internal/config` in initcmd.go). Adjust `TestInitAutoConfigures` in `internal/cli/serve_test.go`: set `t.Setenv("ERBRUS_HOME", tmp)` near the other env setup, and change the two `.erbrus.yaml` assertions to use `config.RepoConfigPath(repo)` (import `erbrus/internal/config` in the test file): the scaffold-exists check after first init, and the no-overwrite check (write `"session: custom\n"` to `config.RepoConfigPath(repo)`, run init again, assert unchanged).

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS everywhere (config, cli, server all compile with the new arity)

- [ ] **Step 5: Commit**

```bash
git add internal/config/ internal/cli/ internal/server/
git commit -m "feat: relocate per-repo config to ~/.erbrus/repos/<encoded-path>"
```

---

### Task 2: API carry-overs — name collision, store-error 500s, worktree re-detection

**Files:**
- Modify: `internal/server/projects.go`, `internal/server/messages.go`, `internal/server/forward.go`, `internal/server/server.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: existing store/wt APIs (`ChannelByName` already exists).
- Produces: behavior changes only, no new exports:
  1. **Name collision:** when `CreateProject(base, root)` fails on the name-unique constraint, retry as `base-2`, `base-3`, … up to `base-9`; give up with 500 after that.
  2. **Store errors are 500s:** `ChannelByID`/`MessageByID` lookups in `handleGetMessages`, `handlePostMessage`, `handleForward` return 500 on `err != nil` (404 stays for genuinely-missing); `bearerRun` distinguishes store error (500) from invalid token (401) — change its signature to `(store.AgentRun, bool, int)` where the int is 0 (ok) or an HTTP status to fail with.
  3. **Re-detection on existing projects:** `handleAddProject` no longer returns early for a known project — it re-runs `wt.List` and creates any channel whose worktree is new (skip existing via `ChannelByName`), honoring `channel_per_worktree`. This is what makes `wt new feature-x && erbrus start reviewer` work (spec: auto-configure step 2).

- [ ] **Step 1: Write the failing tests** (append to `internal/server/server_test.go`)

```go
func TestAddProjectNameCollisionSuffixes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rootA := filepath.Join(t.TempDir(), "webshop")
	rootB := filepath.Join(t.TempDir(), "webshop") // same basename, different parent
	os.MkdirAll(rootA, 0o755)
	os.MkdirAll(rootB, 0o755)
	run := func(dir, name string, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, dir)), nil
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	ts := httptest.NewServer(New(st, cfg, run).Handler())
	defer ts.Close()

	r1 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": rootA})
	p1 := decode[map[string]any](t, r1)
	r2 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": rootB})
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("second same-name project status = %d", r2.StatusCode)
	}
	p2 := decode[map[string]any](t, r2)
	if p1["name"] != "webshop" || p2["name"] != "webshop-2" {
		t.Errorf("names = %v, %v; want webshop, webshop-2", p1["name"], p2["name"])
	}
}

func TestAddProjectRedetectsNewWorktrees(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	// Mutable worktree listing: starts with main only, grows a worktree later.
	listing := fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, root)
	run := func(dir, name string, args ...string) ([]byte, error) { return []byte(listing), nil }
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	ts := httptest.NewServer(New(st, cfg, run).Handler())
	defer ts.Close()

	r1 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p1 := decode[map[string]any](t, r1)
	if n := len(p1["channels"].([]any)); n != 1 {
		t.Fatalf("initial channels = %d, want 1", n)
	}

	listing = fmt.Sprintf(`[
	  {"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true},
	  {"path": %q, "branch": "refs/heads/wt/feat", "head": "b", "is_main": false}
	]`, root, root+"-wt-feat")

	r2 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p2 := decode[map[string]any](t, r2)
	chans := p2["channels"].([]any)
	if len(chans) != 2 {
		t.Fatalf("after re-detection channels = %d, want 2", len(chans))
	}
	if chans[1].(map[string]any)["name"] != "wt/feat" {
		t.Errorf("new channel = %v", chans[1])
	}
	// Third call: no duplicates.
	r3 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p3 := decode[map[string]any](t, r3)
	if len(p3["channels"].([]any)) != 2 {
		t.Error("re-detection must not duplicate channels")
	}
}
```

(Store-error-to-500 paths are refactors of untriggerable-in-tests branches on a healthy SQLite; they are covered by compilation + the existing 404 tests staying green, and reviewed by inspection.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'Collision|Redetect' -v`
Expected: FAIL — second project 500s; channel count stays 1

- [ ] **Step 3: Implement**

`internal/server/projects.go` — name collision (replace the bare `CreateProject` call):

```go
	base := filepath.Base(root)
	var p store.Project
	name := base
	for i := 2; ; i++ {
		var cerr error
		p, cerr = s.st.CreateProject(name, root)
		if cerr == nil {
			break
		}
		if i > 9 {
			httpError(w, http.StatusInternalServerError, cerr.Error())
			return
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
```

Re-detection — restructure `handleAddProject`: after the (error-checked) `ProjectByPath` lookup, BOTH branches run detection; extract a helper used by both:

```go
// ensureChannels runs worktree detection for p and creates any missing
// channels. Returns a warning string ("" when clean).
func (s *Server) ensureChannels(p store.Project) string {
	warning := ""
	wts, err := wt.List(s.run, s.cfg.WtBin, p.RepoPath)
	if err != nil {
		warning = "worktree detection failed: " + err.Error()
	}
	repoCfg, _, _ := config.LoadRepo(p.RepoPath)
	perWorktree := repoCfg.ChannelPerWorktree == nil || *repoCfg.ChannelPerWorktree

	addWarn := func(msg string) {
		if warning == "" {
			warning = msg
		} else {
			warning += "; " + msg
		}
	}
	ensure := func(name, path, branch string) {
		if _, ok, err := s.st.ChannelByName(p.ID, name); err != nil || ok {
			return
		}
		if _, err := s.st.CreateChannel(p.ID, name, path, branch); err != nil {
			addWarn(fmt.Sprintf("failed to create channel %s: %s", name, err))
		}
	}
	ensure("general", p.RepoPath, mainBranch(wts))
	if perWorktree {
		for _, w2 := range wts {
			if w2.IsMain {
				continue
			}
			name := wt.ShortBranch(w2.Branch)
			if name == "" {
				name = filepath.Base(w2.Path)
			}
			ensure(name, w2.Path, w2.Branch)
		}
	}
	return warning
}
```

`handleAddProject` becomes: resolve root → `ProjectByPath` (err→500) → if missing, create with collision loop (created=true) → `warning := s.ensureChannels(p)` → respond. The old inline channel-creation block is deleted (the general-channel 500 case folds into the warning — acceptable: the project row exists either way and the warning surfaces the failure).

Store-error 500s: in `messages.go` `handleGetMessages`/`handlePostMessage` change `if _, ok, _ := s.st.ChannelByID(chID); !ok` to check the error first (500) then ok (404). Same for both lookups in `forward.go`. In `server.go`, `bearerRun` returns `(store.AgentRun, bool, int)`:

```go
func (s *Server) bearerRun(r *http.Request) (store.AgentRun, bool, int) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return store.AgentRun{}, false, 0
	}
	token := strings.TrimPrefix(h, "Bearer ")
	run, ok, err := s.st.RunByToken(token)
	if err != nil {
		return store.AgentRun{}, false, http.StatusInternalServerError
	}
	if !ok {
		return store.AgentRun{}, false, http.StatusUnauthorized
	}
	return run, true, 0
}
```

and its caller in `handlePostMessage`:

```go
	run, isAgent, failStatus := s.bearerRun(r)
	if failStatus != 0 {
		httpError(w, failStatus, "invalid run token")
		return
	}
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS — including all Plan-1 server tests (idempotency test still passes: re-detection creates nothing new when the listing is unchanged)

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat: project-name collision suffixes, worktree re-detection, store-error 500s"
```

---

### Task 3: TmuxSpawner (internal/spawn)

**Files:**
- Create: `internal/spawn/spawn.go`, `internal/spawn/tmux.go`
- Test: `internal/spawn/tmux_test.go`

**Interfaces:**
- Consumes: nothing (exec via injectable runner).
- Produces:

```go
// CmdRunner executes a command and returns combined stdout. Production:
// ExecCmdRunner. Tests inject a recorder.
type CmdRunner func(name string, args ...string) ([]byte, error)
func ExecCmdRunner(name string, args ...string) ([]byte, error)

type RunSpec struct {
	Session       string            // managed session name (used when AttachSession == "")
	AttachSession string            // non-empty: open the window in this existing session
	WindowName    string
	Workdir       string
	Env           map[string]string // injected via `new-window -e K=V`, keys sorted
	Command       []string          // argv, e.g. {"/abs/erbrus", "wrap", "/data/runs/7/cmd.sh"}
}
type Handle string // tmux target "session:windowIndex"
type Spawner interface {
	Spawn(spec RunSpec) (Handle, error)
	Stop(h Handle) error
	Alive(h Handle) (bool, error)
}
func NewTmux(run CmdRunner) *Tmux
```

Tmux behavior (the contract the tests pin):

- Managed mode (`AttachSession == ""`): `tmux has-session -t <Session>`; on error → `tmux new-session -d -s <Session> -c <Workdir>`. Attach mode: never creates the session; `has-session` failure is a hard error ("attach_session <name> not found").
- Window: `tmux new-window -d -P -F #{session_name}:#{window_index} -t <session>: -n <WindowName> -c <Workdir> [-e K=V ...sorted] -- <Command...>`; trimmed stdout is the Handle.
- After spawn: `tmux set-option -t <handle> remain-on-exit on`.
- `Stop`: `tmux kill-window -t <handle>` (always our own window — never touches anything erbrus didn't create).
- `Alive`: `tmux list-windows -t <session-part-of-handle> -F #{session_name}:#{window_index}`, true iff a line equals the handle; a `list-windows` exec error (session gone) is `(false, nil)`.

- [ ] **Step 1: Write the failing tests**

`internal/spawn/tmux_test.go`:

```go
package spawn

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recorder captures every command and replays scripted responses.
type recorder struct {
	calls []string
	// responses keyed by command prefix ("tmux has-session"), value: output or error
	fail map[string]error
	out  map[string]string
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	for prefix, err := range r.fail {
		if strings.HasPrefix(call, prefix) {
			return nil, err
		}
	}
	for prefix, out := range r.out {
		if strings.HasPrefix(call, prefix) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

func spec() RunSpec {
	return RunSpec{
		Session:    "erbrus-webshop",
		WindowName: "review-kimi",
		Workdir:    "/code/webshop",
		Env:        map[string]string{"ERBRUS_URL": "http://127.0.0.1:7420", "ERBRUS_CHANNEL": "3"},
		Command:    []string{"/abs/erbrus", "wrap", "/data/runs/7/cmd.sh"},
	}
}

func TestSpawnManagedCreatesSession(t *testing.T) {
	rec := &recorder{
		fail: map[string]error{"tmux has-session": errors.New("no session")},
		out:  map[string]string{"tmux new-window": "erbrus-webshop:1\n"},
	}
	h, err := NewTmux(rec.run).Spawn(spec())
	if err != nil {
		t.Fatal(err)
	}
	if h != "erbrus-webshop:1" {
		t.Errorf("handle = %q", h)
	}
	want := []string{
		"tmux has-session -t erbrus-webshop",
		"tmux new-session -d -s erbrus-webshop -c /code/webshop",
		"tmux new-window -d -P -F #{session_name}:#{window_index} -t erbrus-webshop: -n review-kimi -c /code/webshop -e ERBRUS_CHANNEL=3 -e ERBRUS_URL=http://127.0.0.1:7420 -- /abs/erbrus wrap /data/runs/7/cmd.sh",
		"tmux set-option -t erbrus-webshop:1 remain-on-exit on",
	}
	if len(rec.calls) != len(want) {
		t.Fatalf("calls = %v", rec.calls)
	}
	for i := range want {
		if rec.calls[i] != want[i] {
			t.Errorf("call %d:\n got  %q\n want %q", i, rec.calls[i], want[i])
		}
	}
}

func TestSpawnManagedReusesSession(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux new-window": "erbrus-webshop:4\n"}}
	if _, err := NewTmux(rec.run).Spawn(spec()); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "tmux new-session") {
			t.Error("must not create a session that exists")
		}
	}
}

func TestSpawnAttachMode(t *testing.T) {
	s := spec()
	s.AttachSession = "main"
	rec := &recorder{out: map[string]string{"tmux new-window": "main:9\n"}}
	h, err := NewTmux(rec.run).Spawn(s)
	if err != nil {
		t.Fatal(err)
	}
	if h != "main:9" {
		t.Errorf("handle = %q", h)
	}
	if !strings.Contains(rec.calls[1], "-t main:") {
		t.Errorf("window not opened in attach session: %v", rec.calls)
	}
}

func TestSpawnAttachMissingSessionFails(t *testing.T) {
	s := spec()
	s.AttachSession = "gone"
	rec := &recorder{fail: map[string]error{"tmux has-session": errors.New("no session")}}
	if _, err := NewTmux(rec.run).Spawn(s); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("want attach failure naming the session, got %v", err)
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "tmux new-session") {
			t.Error("attach mode must never create sessions")
		}
	}
}

func TestStopKillsWindow(t *testing.T) {
	rec := &recorder{}
	if err := NewTmux(rec.run).Stop("erbrus-webshop:3"); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0] != "tmux kill-window -t erbrus-webshop:3" {
		t.Errorf("calls = %v", rec.calls)
	}
}

func TestAlive(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux list-windows": "erbrus-webshop:1\nerbrus-webshop:3\n"}}
	tm := NewTmux(rec.run)
	if ok, _ := tm.Alive("erbrus-webshop:3"); !ok {
		t.Error("want alive")
	}
	if ok, _ := tm.Alive("erbrus-webshop:9"); ok {
		t.Error("want dead")
	}
	rec2 := &recorder{fail: map[string]error{"tmux list-windows": errors.New("no session")}}
	ok, err := NewTmux(rec2.run).Alive("gone:1")
	if err != nil || ok {
		t.Errorf("gone session => (false, nil), got (%v, %v)", ok, err)
	}
	_ = fmt.Sprint()
}
```

(Drop the trailing `_ = fmt.Sprint()` and the `fmt` import if unused — verify imports as always.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/spawn/ -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement**

`internal/spawn/spawn.go`:

```go
// Package spawn launches agent runs into terminal multiplexers. TmuxSpawner
// is the v1 implementation; zellij/headless are future Spawners. erbrus
// only ever kills windows it created.
package spawn

import "os/exec"

type CmdRunner func(name string, args ...string) ([]byte, error)

func ExecCmdRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type RunSpec struct {
	Session       string
	AttachSession string
	WindowName    string
	Workdir       string
	Env           map[string]string
	Command       []string
}

type Handle string

type Spawner interface {
	Spawn(spec RunSpec) (Handle, error)
	Stop(h Handle) error
	Alive(h Handle) (bool, error)
}
```

`internal/spawn/tmux.go`:

```go
package spawn

import (
	"fmt"
	"sort"
	"strings"
)

type Tmux struct{ run CmdRunner }

func NewTmux(run CmdRunner) *Tmux { return &Tmux{run: run} }

func (t *Tmux) Spawn(spec RunSpec) (Handle, error) {
	session := spec.Session
	if spec.AttachSession != "" {
		session = spec.AttachSession
		if _, err := t.run("tmux", "has-session", "-t", session); err != nil {
			return "", fmt.Errorf("attach_session %q not found: %w", session, err)
		}
	} else {
		if _, err := t.run("tmux", "has-session", "-t", session); err != nil {
			if _, err := t.run("tmux", "new-session", "-d", "-s", session, "-c", spec.Workdir); err != nil {
				return "", fmt.Errorf("create session %q: %w", session, err)
			}
		}
	}

	args := []string{"new-window", "-d", "-P", "-F", "#{session_name}:#{window_index}",
		"-t", session + ":", "-n", spec.WindowName, "-c", spec.Workdir}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, "--")
	args = append(args, spec.Command...)
	out, err := t.run("tmux", args...)
	if err != nil {
		return "", fmt.Errorf("open window: %w", err)
	}
	h := Handle(strings.TrimSpace(string(out)))
	if _, err := t.run("tmux", "set-option", "-t", string(h), "remain-on-exit", "on"); err != nil {
		return h, nil // cosmetic option; the run is already up
	}
	return h, nil
}

func (t *Tmux) Stop(h Handle) error {
	_, err := t.run("tmux", "kill-window", "-t", string(h))
	return err
}

func (t *Tmux) Alive(h Handle) (bool, error) {
	session, _, ok := strings.Cut(string(h), ":")
	if !ok {
		return false, fmt.Errorf("malformed handle %q", h)
	}
	out, err := t.run("tmux", "list-windows", "-t", session, "-F", "#{session_name}:#{window_index}")
	if err != nil {
		return false, nil // session gone => not alive, not an error
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == string(h) {
			return true, nil
		}
	}
	return false, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/spawn/ -v`
Expected: PASS (6 tests) — the argv assertions are exact; fix the code, not the test, on mismatch

- [ ] **Step 5: Commit**

```bash
git add internal/spawn/
git commit -m "feat: tmux spawner with managed and attach modes"
```

---

### Task 4: `erbrus wrap` — exit reporting for every provider

**Files:**
- Create: `internal/cli/wrap.go`
- Modify: `internal/cli/cli.go` (dispatch `wrap`, keep it out of the usage text — internal mode), `internal/client/client.go` (add `ReportExit`)
- Test: `internal/cli/wrap_test.go`, `internal/client/client_test.go`

**Interfaces:**
- Consumes: env `ERBRUS_URL`, `ERBRUS_TOKEN`, `ERBRUS_RUN_ID`; the runs-exit endpoint (Task 6 implements the server side — until then the client method is tested against a stub handler).
- Produces:

```go
// client:
func (c *Client) ReportExit(runID int64, code int) error   // POST /api/runs/{id}/exit {"code": N}, bearer token
// cli: erbrus wrap <cmdfile> — runs `sh <cmdfile>` with inherited stdio,
// POSTs the exit status (best-effort: a report failure is printed to stderr
// but never masks the exit code), returns the child's exit code.
func runWrap(args []string, stdout, stderr io.Writer) int
```

- [ ] **Step 1: Write the failing tests**

Append to `internal/client/client_test.go`:

```go
func TestReportExit(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok123")
	if err := c.ReportExit(7, 3); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/runs/7/exit" || gotAuth != "Bearer tok123" || gotBody["code"] != 3 {
		t.Errorf("path=%q auth=%q body=%v", gotPath, gotAuth, gotBody)
	}
}
```

(new imports in that test file: `encoding/json`, `net/http`.)

`internal/cli/wrap_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCmdFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cmd.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func wrapEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_TOKEN", "tok")
	t.Setenv("ERBRUS_RUN_ID", "7")
}

func TestWrapReportsZeroExit(t *testing.T) {
	var gotCode = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		gotCode = body["code"]
	}))
	defer srv.Close()
	wrapEnv(t, srv.URL)
	var out, errOut bytes.Buffer
	code := Run([]string{"wrap", writeCmdFile(t, "echo hello; exit 0")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("child stdout not inherited: %q", out.String())
	}
	if gotCode != 0 {
		t.Errorf("reported code = %d", gotCode)
	}
}

func TestWrapPropagatesNonZeroExit(t *testing.T) {
	var gotCode = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		gotCode = body["code"]
	}))
	defer srv.Close()
	wrapEnv(t, srv.URL)
	var out, errOut bytes.Buffer
	code := Run([]string{"wrap", writeCmdFile(t, "exit 3")}, &out, &errOut)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if gotCode != 3 {
		t.Errorf("reported code = %d", gotCode)
	}
}

func TestWrapReportFailureDoesNotMaskExit(t *testing.T) {
	wrapEnv(t, "http://127.0.0.1:1") // nothing listening
	var out, errOut bytes.Buffer
	code := Run([]string{"wrap", writeCmdFile(t, "exit 0")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 despite report failure", code)
	}
	if !strings.Contains(errOut.String(), "report") {
		t.Errorf("stderr should mention the failed report: %q", errOut.String())
	}
	_ = fmt.Sprint()
}

func TestWrapMissingArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"wrap"}, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
```

(Drop `_ = fmt.Sprint()` + unused imports at implementation time.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run Wrap -v && go test ./internal/client/ -run ReportExit -v`
Expected: FAIL — unknown command / undefined method

- [ ] **Step 3: Implement**

`internal/client/client.go`:

```go
// ReportExit posts a run's exit status; used by the wrap mode.
func (c *Client) ReportExit(runID int64, code int) error {
	return c.postJSON(fmt.Sprintf("/api/runs/%d/exit", runID), map[string]int{"code": code}, nil)
}
```

`internal/cli/wrap.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"erbrus/internal/client"
)

// runWrap executes a run's cmd.sh and reports the exit status back to the
// server. It is spawned inside the tmux window (or by `start --fg`) and is
// deliberately absent from the usage text.
func runWrap(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: erbrus wrap <cmdfile>")
		return 2
	}
	cmd := exec.Command("sh", args[0])
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			fmt.Fprintln(stderr, "wrap:", err)
			code = 127
		}
	}

	runID, _ := strconv.ParseInt(os.Getenv("ERBRUS_RUN_ID"), 10, 64)
	c, _, _ := client.FromEnv()
	if runID != 0 {
		if err := c.ReportExit(runID, code); err != nil {
			fmt.Fprintln(stderr, "wrap: exit report failed:", err)
		}
	}
	return code
}
```

Wire into `cli.Run`: `case "wrap": return runWrap(args[1:], stdout, stderr)` (do NOT add wrap to the usage text).

- [ ] **Step 4: Run the suite**

Run: `go test ./internal/cli/ ./internal/client/ -v && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ internal/client/
git commit -m "feat: wrap mode reporting run exit status"
```

---

### Task 5: Agent integration (internal/integrate)

**Files:**
- Create: `internal/integrate/integrate.go`
- Test: `internal/integrate/integrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// Preamble tells a spawned agent who it is and how to report. erbrusBin is
// ALWAYS an absolute path.
func Preamble(erbrusBin, agentName, channelName string) string
// AssemblePrompt joins preamble, task prompt, and optional handoff context
// with blank lines; empty parts are skipped.
func AssemblePrompt(preamble, prompt, context string) string
// HandoffContext renders a report into prompt context: provenance line,
// body, and each artifact's absolute path (contents never inlined).
func HandoffContext(project, channel, author, createdAt, body string, artifactPaths []string) string
// ProviderHook writes provider-specific hook files into runDir and returns
// extra CLI args to append ("" when the provider gets no hook).
// - "claude-code": writes claude-settings.json (Stop hook posting a system
//   message via erbrusBin), returns `--settings <path>`.
// - "codex": writes notify.sh (posts a system message), returns the codex
//   notify config args. IMPLEMENTATION NOTE: verify the exact syntax against
//   the installed codex binary; if unverifiable on this machine, return ""
//   for codex and say so in the task report.
// - anything else: "" (preamble + exit wrapper only).
func ProviderHook(provider, runDir, erbrusBin string) (extraArgs string, err error)
```

Required preamble content (tests assert substrings): the agent's name; the channel name; `<erbrusBin> msg send --report` and `<erbrusBin> msg read` spelled with the absolute binary path; a line that flags come BEFORE the message text; an instruction to post a report (with `--file` for artifacts) as the final step.

- [ ] **Step 1: Write the failing tests**

`internal/integrate/integrate_test.go`:

```go
package integrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreamble(t *testing.T) {
	p := Preamble("/abs/bin/erbrus", "review-kimi", "wt/feat")
	for _, want := range []string{
		"review-kimi",
		"wt/feat",
		"/abs/bin/erbrus msg send --report",
		"/abs/bin/erbrus msg read",
		"before the message text",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("preamble missing %q\n%s", want, p)
		}
	}
}

func TestAssemblePrompt(t *testing.T) {
	got := AssemblePrompt("PRE", "TASK", "CTX")
	if got != "PRE\n\nTASK\n\nCTX" {
		t.Errorf("got %q", got)
	}
	if got := AssemblePrompt("PRE", "TASK", ""); got != "PRE\n\nTASK" {
		t.Errorf("empty context: %q", got)
	}
}

func TestHandoffContext(t *testing.T) {
	got := HandoffContext("webshop", "wt/feat", "impl-codex", "2026-08-27 10:00:00",
		"6 files changed", []string{"/data/artifacts/12/analysis.md"})
	for _, want := range []string{"webshop", "wt/feat", "impl-codex", "6 files changed", "/data/artifacts/12/analysis.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("context missing %q\n%s", want, got)
		}
	}
}

func TestProviderHookClaude(t *testing.T) {
	dir := t.TempDir()
	args, err := ProviderHook("claude-code", dir, "/abs/erbrus")
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "claude-settings.json")
	if args != "--settings "+settings {
		t.Errorf("args = %q", args)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("settings not valid JSON: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "/abs/erbrus msg send --system") {
		t.Errorf("stop hook must invoke the absolute binary:\n%s", data)
	}
	if !strings.Contains(string(data), "Stop") {
		t.Errorf("no Stop hook in settings:\n%s", data)
	}
}

func TestProviderHookUnknownProvider(t *testing.T) {
	args, err := ProviderHook("junie", t.TempDir(), "/abs/erbrus")
	if err != nil || args != "" {
		t.Errorf("unknown provider must be a no-op, got %q, %v", args, err)
	}
}
```

(Codex hook behavior is asserted by whatever the implementer verifies against the installed binary — add a test mirroring the claude one if codex wiring lands, else none.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/integrate/ -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Implement**

`internal/integrate/integrate.go`:

```go
// Package integrate wires spawned agents to erbrus: an instruction preamble
// every provider gets, and per-provider hooks where the CLI supports them.
package integrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Preamble(erbrusBin, agentName, channelName string) string {
	return fmt.Sprintf(`You are agent %q working in the erbrus channel #%s.
Report progress and results to the channel with this exact command (flags go before the message text):
  %s msg send --report "what you did"
Attach a produced document with --file:
  %s msg send --report --file /abs/path/to/report.md "summary"
Read the channel history with:
  %s msg read
Post a report as your FINAL step — the next agent picks up from it.`,
		agentName, channelName, erbrusBin, erbrusBin, erbrusBin)
}

func AssemblePrompt(preamble, prompt, context string) string {
	parts := []string{}
	for _, p := range []string{preamble, prompt, context} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

func HandoffContext(project, channel, author, createdAt, body string, artifactPaths []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- context: report from %s / #%s by %s at %s ---\n", project, channel, author, createdAt)
	b.WriteString(body)
	for _, p := range artifactPaths {
		fmt.Fprintf(&b, "\nattached file (read it yourself): %s", p)
	}
	return b.String()
}

func ProviderHook(provider, runDir, erbrusBin string) (string, error) {
	switch provider {
	case "claude-code":
		return claudeHook(runDir, erbrusBin)
	case "codex":
		// Verified against the installed codex binary at implementation
		// time; see the task note. Falls through to no-hook when the
		// notify syntax could not be confirmed.
		return codexHook(runDir, erbrusBin)
	default:
		return "", nil
	}
}

func claudeHook(runDir, erbrusBin string) (string, error) {
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": fmt.Sprintf(`%s msg send --system "claude stop hook: prompt finished"`, erbrusBin),
				}},
			}},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(runDir, "claude-settings.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return "--settings " + path, nil
}
```

`codexHook`: the implementer verifies the installed codex CLI's notify mechanism (`codex --help`, `codex config --help`, or its config docs). If confirmed, write `<runDir>/notify.sh` (`#!/bin/sh` + `exec <erbrusBin> msg send --system "codex notify: turn complete"`, mode 0755) and return the matching `-c notify=[...]`-style args; if not confirmable, `return "", nil` and record that in the task report. Either way the exit wrapper still reports completion for codex.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/integrate/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/integrate/
git commit -m "feat: agent integration preamble and provider hooks"
```

---

### Task 6: Runs API — spawn (internal/server/runs.go)

**Files:**
- Create: `internal/server/runs.go`
- Modify: `internal/server/server.go` (SetRuntime, routes)
- Test: `internal/server/runs_test.go`

**Interfaces:**
- Consumes: `spawn.Spawner`/`RunSpec`/`Handle`, `preset.Merge`/`Resolve`, `provider.Registry`, `integrate.*`, `config.LoadRepo`, store runs CRUD.
- Produces:

```go
func (s *Server) SetRuntime(sp spawn.Spawner, erbrusBin, baseURL string)  // serve calls this; nil-safe fields on Server
// Routes (inside /api):
// POST /runs                {channel_id, preset?, provider?, name?, model?, args?, prompt?, workdir?, fg?, origin_message_id?}
//   -> 201 runJSON (tmux path) | 201 fgJSON (fg path) | 400/404/500/503
// GET  /channels/{id}/runs  -> 200 [runJSON]
type runJSON struct {
	ID         int64  `json:"id"`
	ChannelID  int64  `json:"channel_id"`
	Provider   string `json:"provider"`
	AgentName  string `json:"agent_name"`
	Status     string `json:"status"`
	TmuxTarget string `json:"tmux_target,omitempty"`
	ExitCode   *int64 `json:"exit_code,omitempty"`
}
type fgJSON struct {
	Run     runJSON           `json:"run"`
	CmdFile string            `json:"cmd_file"`
	Env     map[string]string `json:"env"`
}
```

Spawn algorithm (the contract):

1. Channel lookup (err→500, missing→404) → project via `ProjectByID` (err→500).
2. `config.LoadRepo(project.RepoPath)`; presets = `preset.Merge(cfg.Presets, repoCfg.Presets)`. If `preset` given: `preset.Resolve` (unknown→400 with its message). Explicit request fields override preset fields; `provider` required after overlay (else 400); provider must exist in `cfg.Providers` (400). Agent name default chain: request `name` → preset `Name` → preset key → provider name.
3. Workdir default: request `workdir` → channel.WorktreePath → project.RepoPath.
4. `CreateRun` (status `starting`, spawner `tmux` or `fg` per request).
5. `runDir := <dataDir>/runs/<id>` (MkdirAll). Handoff: when `origin_message_id` set, load the message (missing→404) + its artifacts; context = `integrate.HandoffContext(project.Name, <origin channel name>, msg.AuthorName, msg.CreatedAt formatted, msg.Body, artifact paths)`; post a system message to the ORIGIN channel: `"report handed off to #<target channel> (run <id>)"`.
6. `hookArgs, _ := integrate.ProviderHook(provider, runDir, s.erbrusBin)`; args = joinNonEmpty(requestOrPresetArgs, hookArgs, " ").
7. Full prompt = `integrate.AssemblePrompt(integrate.Preamble(s.erbrusBin, agentName, channel.Name), promptText, context)`; command = `provider.Registry(cfg.Providers).Render(providerName, model, args, fullPrompt)`.
8. Write `<runDir>/cmd.sh`: `#!/bin/sh\nexec <command>\n` (0755).
9. Env: `ERBRUS_URL`=s.baseURL, `ERBRUS_TOKEN`=run.Token, `ERBRUS_CHANNEL`=channelID, `ERBRUS_RUN_ID`=runID.
10. `fg`: respond 201 `fgJSON` (run stays `starting`; the CLI flips it by reporting exit; no spawner needed).
11. tmux: 503 if `s.spawner == nil`. Session = repoCfg.Session, else `cfg.SessionPattern` with `{project}` → project.Name; AttachSession = repoCfg.AttachSession. Spawn; on error → `FinishRun(failed, -1)` + system message `"spawn failed: <err>"` + 500. On success: update run (`status=running`, `tmux_target=handle` — add a small `store.StartRun(id int64, tmuxTarget string) error` UPDATE for this), system message `"<agentName> spawned in tmux <handle>"`, `hub.Publish("run", runJSON)`, respond 201.

- [ ] **Step 1: Write the failing tests**

`internal/server/runs_test.go` (reuses `newTestServer` helpers; add a fake spawner):

```go
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/spawn"
)

type fakeSpawner struct {
	specs  []spawn.RunSpec
	handle spawn.Handle
	err    error
	killed []spawn.Handle
	alive  map[spawn.Handle]bool
}

func (f *fakeSpawner) Spawn(s spawn.RunSpec) (spawn.Handle, error) {
	f.specs = append(f.specs, s)
	return f.handle, f.err
}
func (f *fakeSpawner) Stop(h spawn.Handle) error { f.killed = append(f.killed, h); return nil }
func (f *fakeSpawner) Alive(h spawn.Handle) (bool, error) { return f.alive[h], nil }

func TestSpawnRunTmux(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	fs := &fakeSpawner{handle: "erbrus-x:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL) // see note below on exposing the server

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "provider": "codex", "prompt": "do it",
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	run := decode[map[string]any](t, rresp)
	if run["status"] != "running" || run["tmux_target"] != "erbrus-x:1" {
		t.Fatalf("run = %v", run)
	}

	if len(fs.specs) != 1 {
		t.Fatal("spawner not called")
	}
	spec := fs.specs[0]
	if len(spec.Command) != 3 || spec.Command[0] != "/abs/erbrus" || spec.Command[1] != "wrap" {
		t.Errorf("command = %v", spec.Command)
	}
	data, err := os.ReadFile(spec.Command[2])
	if err != nil {
		t.Fatalf("cmd.sh unreadable: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "#!/bin/sh\nexec codex ") {
		t.Errorf("cmd.sh = %q", content)
	}
	if !strings.Contains(content, "do it") || !strings.Contains(content, "msg send --report") {
		t.Errorf("prompt/preamble missing from command: %q", content)
	}
	if spec.Env["ERBRUS_URL"] != ts.URL || spec.Env["ERBRUS_CHANNEL"] != fmt.Sprint(chID) {
		t.Errorf("env = %v", spec.Env)
	}
	if spec.Env["ERBRUS_TOKEN"] == "" || spec.Env["ERBRUS_RUN_ID"] == "" {
		t.Errorf("token/run id env missing: %v", spec.Env)
	}
	if spec.Session != "erbrus-"+p["name"].(string) {
		t.Errorf("session = %q", spec.Session)
	}

	// A system message announced the spawn.
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Kind == "system" && strings.Contains(m.Body, "spawned in tmux erbrus-x:1") {
			found = true
		}
	}
	if !found {
		t.Errorf("no spawn system message: %+v", msgs)
	}
}

func TestSpawnRunPresetAndOverrides(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "preset": "kimi", "model": "k3-override",
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	run := decode[map[string]any](t, rresp)
	if run["provider"] != "kimi" || run["agent_name"] != "kimi" {
		t.Errorf("preset not applied: %v", run)
	}
	data, _ := os.ReadFile(fs.specs[0].Command[2])
	if !strings.Contains(string(data), "k3-override") {
		t.Errorf("model override not rendered: %q", data)
	}
}

func TestSpawnRunValidation(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	for name, body := range map[string]map[string]any{
		"unknown channel": {"channel_id": 999, "provider": "codex"},
		"no provider":     {"channel_id": chID},
		"unknown preset":  {"channel_id": chID, "preset": "nope"},
		"unknown provider": {"channel_id": chID, "provider": "nope"},
	} {
		r := postJSON(t, ts.URL+"/api/runs", body)
		if r.StatusCode != http.StatusBadRequest && r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 400/404", name, r.StatusCode)
		}
		r.Body.Close()
	}
}

func TestSpawnRunNoSpawnerIs503(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	// No SetRuntime call — default server has no spawner.
	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", r.StatusCode)
	}
	r.Body.Close()
}

func TestSpawnRunFg(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	// fg needs no spawner.
	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex", "fg": true})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", r.StatusCode)
	}
	fg := decode[map[string]any](t, r)
	if fg["cmd_file"] == "" || fg["env"].(map[string]any)["ERBRUS_TOKEN"] == "" {
		t.Errorf("fg spec incomplete: %v", fg)
	}
}
```

**Test-helper note for the implementer:** `newTestServer` (Plan 1) constructs the `*Server` inline and returns only the httptest server. For `SetRuntime` access, change it to keep the last-built server in a package-level `var testSrv *Server` (set inside `newTestServer` before `httptest.NewServer`) — a two-line change; all existing tests keep working. The test config must register providers/presets: extend `newTestServer`'s `cfg` with `cfg.Providers = map[string]config.Provider{"codex": {Command: "codex {args} \"{prompt}\""}, "kimi": {Command: "kimi --model {model} {args} \"{prompt}\""}}` and `cfg.Presets = map[string]config.Preset{"kimi": {Provider: "kimi", Model: "k3"}}` (import `erbrus/internal/config` already present).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run SpawnRun -v`
Expected: FAIL — 404 route / undefined SetRuntime

- [ ] **Step 3: Implement**

`internal/server/server.go` additions:

```go
	// fields on Server:
	spawner   spawn.Spawner
	erbrusBin string
	baseURL   string

func (s *Server) SetRuntime(sp spawn.Spawner, erbrusBin, baseURL string) {
	s.spawner, s.erbrusBin, s.baseURL = sp, erbrusBin, baseURL
}
	// routes inside the /api block:
	r.Post("/runs", s.handleSpawnRun)
	r.Get("/channels/{id}/runs", s.handleChannelRuns)
```

`internal/server/runs.go` — implement the algorithm exactly as specified in the Interfaces block above. Key excerpts the implementer writes around:

```go
type runRequest struct {
	ChannelID       int64  `json:"channel_id"`
	Preset          string `json:"preset"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	Model           string `json:"model"`
	Args            string `json:"args"`
	Prompt          string `json:"prompt"`
	Workdir         string `json:"workdir"`
	Fg              bool   `json:"fg"`
	OriginMessageID int64  `json:"origin_message_id"`
}

func toRunJSON(r store.AgentRun) runJSON {
	rj := runJSON{ID: r.ID, ChannelID: r.ChannelID, Provider: r.Provider,
		AgentName: r.AgentName, Status: r.Status, TmuxTarget: r.TmuxTarget}
	if r.HasExit {
		v := r.ExitCode
		rj.ExitCode = &v
	}
	return rj
}

// system posts a system message to a channel and publishes it.
func (s *Server) system(channelID int64, body string) {
	m, err := s.st.CreateMessage(store.Message{ChannelID: channelID, Kind: "system", AuthorKind: "system", Body: body})
	if err == nil {
		s.hub.Publish("message", s.messageJSON(m))
	}
}
```

Overlay helper (explicit beats preset, preset beats zero):

```go
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
```

Store addition (this task, in `internal/store/store.go` + a case in its test):

```go
// StartRun marks a run running and records its tmux target.
func (s *Store) StartRun(id int64, tmuxTarget string) error {
	_, err := s.db.Exec(`UPDATE agent_runs SET status = 'running', tmux_target = ? WHERE id = ?`, tmuxTarget, id)
	return err
}
```

with test (append to `TestRunLifecycle`): after `CreateRun`, `s.StartRun(r.ID, "x:1")`, re-read, assert `Status == "running" && TmuxTarget == "x:1"`.

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/ internal/store/
git commit -m "feat: runs API — spawn into tmux or fg with agent integration"
```

---

### Task 7: Runs API — exit, stop, list, handoff

**Files:**
- Modify: `internal/server/runs.go`, `internal/server/server.go` (routes)
- Test: `internal/server/runs_test.go`

**Interfaces:**
- Consumes: Task 6 internals.
- Produces (routes inside /api):

```
POST /runs/{id}/exit {"code": N}  — bearer token MUST match this run (401 otherwise; the
                                     token stays valid up to and including this call);
                                     FinishRun(done when 0, failed otherwise); system
                                     message "<name> finished · exit N"; SSE "run" event.
POST /runs/{id}/stop              — human endpoint (no token): spawner.Stop(target) when a
                                     tmux target exists and a spawner is set; FinishRun
                                     (stopped, -1); system message; SSE "run" event. 404
                                     unknown run; stopping a finished run is a 409.
GET  /channels/{id}/runs          — 200 [runJSON] (already routed in Task 6; implement here
                                     if stubbed).
```

Handoff behaviors (extend `handleSpawnRun`, spec step 5): `origin_message_id` → context appended + system note in origin channel. Already specified in Task 6's algorithm; THIS task adds its tests.

- [ ] **Step 1: Write the failing tests** (append to `internal/server/runs_test.go`)

```go
func TestRunExitEndpoint(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	run := decode[map[string]any](t, rresp)
	runID := int64(run["id"].(float64))
	stRun, _, _ := st.RunByID(runID)

	// Wrong token: 401.
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/runs/%d/exit", ts.URL, runID),
		strings.NewReader(`{"code": 0}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	r1, _ := http.DefaultClient.Do(req)
	if r1.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", r1.StatusCode)
	}
	r1.Body.Close()

	// Right token: run finishes failed on nonzero code.
	req2, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/runs/%d/exit", ts.URL, runID),
		strings.NewReader(`{"code": 3}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+stRun.Token)
	r2, _ := http.DefaultClient.Do(req2)
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("exit status = %d", r2.StatusCode)
	}
	r2.Body.Close()
	got, _, _ := st.RunByID(runID)
	if got.Status != "failed" || !got.HasExit || got.ExitCode != 3 {
		t.Errorf("run after exit: %+v", got)
	}
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Kind == "system" && strings.Contains(m.Body, "exit 3") {
			found = true
		}
	}
	if !found {
		t.Error("no exit system message")
	}
}

func TestRunStopEndpoint(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:2"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	run := decode[map[string]any](t, rresp)
	runID := int64(run["id"].(float64))

	r := postJSON(t, fmt.Sprintf("%s/api/runs/%d/stop", ts.URL, runID), map[string]any{})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d", r.StatusCode)
	}
	r.Body.Close()
	if len(fs.killed) != 1 || fs.killed[0] != "s:2" {
		t.Errorf("kill not issued: %v", fs.killed)
	}
	got, _, _ := st.RunByID(runID)
	if got.Status != "stopped" {
		t.Errorf("status = %q", got.Status)
	}
	// Second stop: 409.
	r2 := postJSON(t, fmt.Sprintf("%s/api/runs/%d/stop", ts.URL, runID), map[string]any{})
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("double stop status = %d, want 409", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestChannelRunsList(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"}).Body.Close()

	lr, _ := http.Get(fmt.Sprintf("%s/api/channels/%d/runs", ts.URL, chID))
	runs := decode[[]map[string]any](t, lr)
	if len(runs) != 1 || runs[0]["provider"] != "codex" {
		t.Errorf("runs = %v", runs)
	}
}

func TestSpawnFromReportAppendsContext(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src),
		map[string]string{"kind": "report", "body": "the analysis result"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": dst, "provider": "codex", "prompt": "implement it",
		"origin_message_id": msgID,
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	rresp.Body.Close()

	data, _ := os.ReadFile(fs.specs[0].Command[2])
	if !strings.Contains(string(data), "the analysis result") {
		t.Errorf("report body not in prompt: %q", data)
	}
	// Origin channel got the handoff note.
	msgs, _ := st.MessagesSince(src, msgID, 100)
	found := false
	for _, mm := range msgs {
		if mm.Kind == "system" && strings.Contains(mm.Body, "handed off") {
			found = true
		}
	}
	if !found {
		t.Error("no handoff system note in origin channel")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'RunExit|RunStop|ChannelRunsList|SpawnFromReport' -v`
Expected: FAIL — 404 routes / missing behavior

- [ ] **Step 3: Implement**

Routes (server.go, /api block): `r.Post("/runs/{id}/exit", s.handleRunExit)`, `r.Post("/runs/{id}/stop", s.handleRunStop)`. Handlers in runs.go:

```go
func (s *Server) handleRunExit(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	run, ok, err := s.st.RunByID(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token != run.Token {
		httpError(w, http.StatusUnauthorized, "token does not match run")
		return
	}
	var req struct {
		Code int64 `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := "done"
	if req.Code != 0 {
		status = "failed"
	}
	if err := s.st.FinishRun(run.ID, status, req.Code); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.system(run.ChannelID, fmt.Sprintf("%s finished · exit %d", run.AgentName, req.Code))
	got, _, _ := s.st.RunByID(run.ID)
	s.hub.Publish("run", toRunJSON(got))
	writeJSON(w, http.StatusOK, toRunJSON(got))
}

func (s *Server) handleRunStop(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	run, ok, err := s.st.RunByID(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status != "starting" && run.Status != "running" {
		httpError(w, http.StatusConflict, "run already finished")
		return
	}
	if run.TmuxTarget != "" && s.spawner != nil {
		if err := s.spawner.Stop(spawn.Handle(run.TmuxTarget)); err != nil {
			s.system(run.ChannelID, fmt.Sprintf("stop of %s reported: %s", run.AgentName, err))
		}
	}
	if err := s.st.FinishRun(run.ID, "stopped", -1); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.system(run.ChannelID, fmt.Sprintf("%s stopped by user", run.AgentName))
	got, _, _ := s.st.RunByID(run.ID)
	s.hub.Publish("run", toRunJSON(got))
	writeJSON(w, http.StatusOK, toRunJSON(got))
}

func (s *Server) handleChannelRuns(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, err := s.st.ChannelByID(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	runs, err := s.st.RunsByChannel(chID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []runJSON{}
	for _, rr := range runs {
		out = append(out, toRunJSON(rr))
	}
	writeJSON(w, http.StatusOK, out)
}
```

Handoff in `handleSpawnRun` (if not fully landed in Task 6): load origin message (err→500, missing→404), `s.st.ArtifactsByMessage`, build context via `integrate.HandoffContext` (channel name from the origin message's channel), then AFTER a successful spawn/fg response is assured, `s.system(origin.ChannelID, fmt.Sprintf("report handed off to #%s (run %d)", targetChannel.Name, run.ID))`.

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat: run exit/stop endpoints, channel runs list, report handoff"
```

---

### Task 8: Startup reconcile of orphaned runs

**Files:**
- Modify: `internal/server/runs.go` (add `Reconcile`), `internal/cli/serve.go` (call it)
- Test: `internal/server/runs_test.go`

**Interfaces:**
- Produces:

```go
// Reconcile marks tmux runs whose window no longer exists as failed, with a
// system message. fg runs are left alone (their wrapper may still report).
// Called by serve after SetRuntime; no-op when spawner is nil.
func (s *Server) Reconcile() error
```

- [ ] **Step 1: Write the failing test**

```go
func TestReconcileMarksOrphans(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	alive, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "a",
		Workdir: root, Status: "running", Spawner: "tmux", TmuxTarget: "s:1"})
	dead, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "b",
		Workdir: root, Status: "running", Spawner: "tmux", TmuxTarget: "s:2"})
	fgRun, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "c",
		Workdir: root, Status: "starting", Spawner: "fg"})
	// Overwrite targets/status via StartRun where needed:
	st.StartRun(alive.ID, "s:1")
	st.StartRun(dead.ID, "s:2")

	fs := &fakeSpawner{alive: map[spawn.Handle]bool{"s:1": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	if err := testSrv.Reconcile(); err != nil {
		t.Fatal(err)
	}

	a, _, _ := st.RunByID(alive.ID)
	d, _, _ := st.RunByID(dead.ID)
	f, _, _ := st.RunByID(fgRun.ID)
	if a.Status != "running" {
		t.Errorf("alive run flipped: %q", a.Status)
	}
	if d.Status != "failed" {
		t.Errorf("orphan not failed: %q", d.Status)
	}
	if f.Status != "starting" {
		t.Errorf("fg run must be untouched: %q", f.Status)
	}
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Kind == "system" && strings.Contains(m.Body, "orphaned") {
			found = true
		}
	}
	if !found {
		t.Error("no orphan system message")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server/ -run Reconcile -v`
Expected: FAIL — undefined Reconcile

- [ ] **Step 3: Implement**

```go
func (s *Server) Reconcile() error {
	if s.spawner == nil {
		return nil
	}
	runs, err := s.st.RunningRuns()
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Spawner != "tmux" || r.TmuxTarget == "" {
			continue
		}
		ok, err := s.spawner.Alive(spawn.Handle(r.TmuxTarget))
		if err != nil || ok {
			continue
		}
		if err := s.st.FinishRun(r.ID, "failed", -1); err != nil {
			return err
		}
		s.system(r.ChannelID, fmt.Sprintf("%s orphaned (tmux window %s gone) — marked failed", r.AgentName, r.TmuxTarget))
	}
	return nil
}
```

`internal/cli/serve.go` — after constructing the server, before serving:

```go
	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// ln is already bound at this point; its address is the base URL.
	srv.SetRuntime(spawn.NewTmux(spawn.ExecCmdRunner), bin, "http://"+ln.Addr().String())
	if err := srv.Reconcile(); err != nil {
		fmt.Fprintln(stderr, "reconcile:", err)
	}
```

(Reorder serve.go so `net.Listen` happens before `SetRuntime`; imports: `erbrus/internal/spawn`.)

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/ internal/cli/
git commit -m "feat: reconcile orphaned tmux runs at startup"
```

---

### Task 9: `erbrus start`

**Files:**
- Create: `internal/cli/start.go`
- Modify: `internal/cli/cli.go` (dispatch + usage), `internal/client/client.go` (SpawnRun)
- Test: `internal/cli/start_test.go`

**Interfaces:**
- Consumes: runs API, `wt.GitRoot`/`MainRoot`, `client.AddProject`.
- Produces:

```go
// client:
type RunResult struct {          // union of runJSON and fgJSON — Fg fields nil on tmux path
	ID         int64             `json:"id"`
	ChannelID  int64             `json:"channel_id"`
	Provider   string            `json:"provider"`
	AgentName  string            `json:"agent_name"`
	Status     string            `json:"status"`
	TmuxTarget string            `json:"tmux_target,omitempty"`
	Run        *RunResult        `json:"run,omitempty"`      // fg envelope nests the run
	CmdFile    string            `json:"cmd_file,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}
func (c *Client) SpawnRun(req map[string]any) (RunResult, error)   // POST /api/runs

// cli:
// erbrus start <preset> [--model M] [--prompt P] [--args A] [--name N]
//              [--channel C] [--fg] [--no-create]
// - resolves repo main root from cwd; POST /api/projects (auto-configure,
//   which since Task 2 also creates channels for NEW worktrees). With
//   --no-create: ListProjects lookup only; missing project => exit 1.
// - channel: --channel accepts an id (numeric) or a name (matched against
//   the project's channels); default = the channel whose worktree_path is
//   the current worktree root, falling back to "general".
// - announces everything auto-created (project "created" flag, channels)
//   plus "spawned <name> → tmux <target>" and the attach hint
//   `tmux attach -t <session>`; with --fg, sets the returned env and runs
//   the wrap flow in-process (child inherits stdio; exit code propagated;
//   the wrap flow reports exit to the server).
func runStart(args []string, stdout, stderr io.Writer) int
```

- [ ] **Step 1: Write the failing tests**

`internal/cli/start_test.go`:

```go
package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// startTestRepo creates a real git repo and points ERBRUS_* env at a live
// serve instance (pattern from TestInitAutoConfigures).
func startTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "startrepo")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	t.Setenv("ERBRUS_HOME", tmp)
	cfgPath := filepath.Join(tmp, "config.yaml")
	// The "sh" provider makes --fg runs real but instant.
	os.WriteFile(cfgPath, []byte(`port: 0
data_dir: `+tmp+`
providers:
  sh:
    command: 'true {args} "{prompt}"'
presets:
  quick:
    provider: sh
`), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfgPath)

	addrCh := make(chan string, 1)
	serveAddrHook = func(addr string) { addrCh <- addr }
	t.Cleanup(func() { serveAddrHook = nil })
	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"serve"}, &out, &errOut)
	}()
	t.Setenv("ERBRUS_URL", "http://"+<-addrCh)

	oldWd, _ := os.Getwd()
	os.Chdir(repo)
	t.Cleanup(func() { os.Chdir(oldWd) })
	return repo
}

func TestStartFgRunsAndReports(t *testing.T) {
	startTestRepo(t)
	var out, errOut bytes.Buffer
	code := Run([]string{"start", "quick", "--fg", "--prompt", "do nothing"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "created project startrepo") {
		t.Errorf("auto-configure not announced: %q", out.String())
	}
	if !strings.Contains(out.String(), "exit 0") {
		t.Errorf("fg completion not announced: %q", out.String())
	}
}

func TestStartUnknownPreset(t *testing.T) {
	startTestRepo(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"start", "nope", "--fg"}, &out, &errOut); code == 0 {
		t.Fatal("want non-zero exit")
	}
	if !strings.Contains(errOut.String(), "nope") {
		t.Errorf("stderr should name the preset: %q", errOut.String())
	}
}

func TestStartNoCreateFailsOnUnknownRepo(t *testing.T) {
	startTestRepo(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"start", "quick", "--fg", "--no-create"}, &out, &errOut); code == 0 {
		t.Fatal("want failure: project not registered and creation disabled")
	}
	if !strings.Contains(errOut.String(), "no-create") && !strings.Contains(errOut.String(), "not registered") {
		t.Errorf("stderr: %q", errOut.String())
	}
	_ = fmt.Sprint()
}

func TestStartRequiresServer(t *testing.T) {
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:1")
	var out, errOut bytes.Buffer
	if code := Run([]string{"start", "quick"}, &out, &errOut); code == 0 {
		t.Fatal("want failure without server")
	}
	if !strings.Contains(errOut.String(), "erbrus serve") {
		t.Errorf("stderr should suggest erbrus serve: %q", errOut.String())
	}
}
```

(As always: drop unused imports/vestigial lines at implementation time. `TestStartRequiresServer` must run OUTSIDE a registered repo context — it relies on the unreachable-server error surfacing from the first API call; if cwd matters, `os.Chdir(t.TempDir())` first — a non-git dir also exercises the "not inside a git repository" path, so assert on either message and adjust to whichever error the flow hits first: the plan accepts `GitRoot` failing before the HTTP call, in which case assert "git" instead. Implementer: pick ONE deterministic setup — `os.Chdir(t.TempDir())` plus asserting the git-root error is the simplest — and keep the test name honest by renaming it TestStartOutsideRepo if you choose that route.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run Start -v`
Expected: FAIL — unknown command

- [ ] **Step 3: Implement**

`internal/client/client.go`:

```go
func (c *Client) SpawnRun(req map[string]any) (RunResult, error) {
	var res RunResult
	err := c.postJSON("/api/runs", req, &res)
	return res, err
}
```

`internal/cli/start.go`:

```go
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"erbrus/internal/client"
	"erbrus/internal/wt"
)

func runStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", "", "model override")
	prompt := fs.String("prompt", "", "prompt override")
	extraArgs := fs.String("args", "", "extra CLI args for the provider")
	name := fs.String("name", "", "agent name override")
	channel := fs.String("channel", "", "channel id or name (default: current worktree's channel)")
	fg := fs.Bool("fg", false, "run in this terminal instead of tmux")
	noCreate := fs.Bool("no-create", false, "fail instead of auto-configuring")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: erbrus start <preset> [flags]")
		return 2
	}
	presetName := fs.Arg(0)

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	worktreeRoot, err := wt.GitRoot(wt.ExecRunner, cwd)
	if err != nil {
		fmt.Fprintln(stderr, "not inside a git repository:", err)
		return 1
	}
	mainRoot, err := wt.MainRoot(wt.ExecRunner, cwd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	c, _, _ := client.FromEnv()
	var project client.Project
	if *noCreate {
		projects, err := c.ListProjects()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		found := false
		for _, p := range projects {
			if p.RepoPath == mainRoot {
				project, found = p, true
			}
		}
		if !found {
			fmt.Fprintf(stderr, "project %s not registered and --no-create given\n", mainRoot)
			return 1
		}
	} else {
		project, err = c.AddProject(mainRoot)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if project.Created {
			fmt.Fprintf(stdout, "created project %s (%s)\n", project.Name, project.RepoPath)
		}
		if project.Warning != "" {
			fmt.Fprintln(stdout, "warning:", project.Warning)
		}
	}

	ch, err := pickChannel(project, worktreeRoot, *channel)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	req := map[string]any{"channel_id": ch.ID, "preset": presetName, "fg": *fg}
	for k, v := range map[string]string{"model": *model, "prompt": *prompt, "args": *extraArgs, "name": *name} {
		if v != "" {
			req[k] = v
		}
	}
	res, err := c.SpawnRun(req)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if !*fg {
		fmt.Fprintf(stdout, "spawned %s → tmux %s (channel #%s)\n", res.AgentName, res.TmuxTarget, ch.Name)
		if sess, _, ok := cutHandle(res.TmuxTarget); ok {
			fmt.Fprintf(stdout, "attach: tmux attach -t %s\n", sess)
		}
		return 0
	}

	// fg: run the command here, wrap-style. Env comes from the server.
	for k, v := range res.Env {
		os.Setenv(k, v)
	}
	fmt.Fprintf(stdout, "running %s in this terminal (channel #%s)\n", res.Run.AgentName, ch.Name)
	code := runWrap([]string{res.CmdFile}, stdout, stderr)
	fmt.Fprintf(stdout, "%s finished · exit %d\n", res.Run.AgentName, code)
	return code
}

func pickChannel(p client.Project, worktreeRoot, override string) (client.Channel, error) {
	if override != "" {
		if id, err := strconv.ParseInt(override, 10, 64); err == nil {
			for _, ch := range p.Channels {
				if ch.ID == id {
					return ch, nil
				}
			}
		}
		for _, ch := range p.Channels {
			if ch.Name == override {
				return ch, nil
			}
		}
		return client.Channel{}, fmt.Errorf("channel %q not found in project %s", override, p.Name)
	}
	for _, ch := range p.Channels {
		if ch.WorktreePath == worktreeRoot {
			return ch, nil
		}
	}
	for _, ch := range p.Channels {
		if ch.Name == "general" {
			return ch, nil
		}
	}
	return client.Channel{}, fmt.Errorf("no channel for worktree %s and no general channel", worktreeRoot)
}

func cutHandle(h string) (string, string, bool) {
	for i := 0; i < len(h); i++ {
		if h[i] == ':' {
			return h[:i], h[i+1:], true
		}
	}
	return "", "", false
}
```

Wire into `cli.Run`: `case "start": return runStart(args[1:], stdout, stderr)`; add the start line to the usage text.

Note for the implementer: the fg response nests the run under `run` (fgJSON), so `res.Run` is non-nil on the fg path and nil on the tmux path — the RunResult struct in the client models both shapes; guard accordingly.

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS (start tests exercise a real serve + real git repo + fg run of `true`)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ internal/client/
git commit -m "feat: erbrus start — preset spawn with auto-configure and --fg"
```

---

### Task 10: Build + handoff (controller step)

- [ ] **Step 1: Full verification**

```bash
go build ./... && go vet ./... && go test -race ./...
```

Expected: all green.

- [ ] **Step 2: Build the binary**

```bash
mkdir -p bin && go build -o bin/erbrus ./cmd/erbrus
```

Hand the user the absolute path of `bin/erbrus` in the worktree, plus this manual test script:

```bash
# terminal 1
<worktree>/bin/erbrus serve

# terminal 2, inside any git repo
<worktree>/bin/erbrus start claude            # spawns Claude Code into tmux erbrus-<project>
tmux attach -t erbrus-<project>               # watch it work; agent posts to the channel
ERBRUS_CHANNEL=1 <worktree>/bin/erbrus msg read
<worktree>/bin/erbrus start codex --fg --prompt "say hello and post a report"   # runs right here
curl -s http://127.0.0.1:7420/api/channels/1/runs | head
```

(Presets `claude`/`codex`/`kimi` must exist in `~/.config/erbrus/config.yaml` — `erbrus init`'s scaffold and the spec's example config show the shape.)

Do NOT merge to main. Do NOT push. Stop and wait for the user's manual-test verdict; Plan 3 (web UI) is written after it.

---

## Self-review notes (already applied)

- Spec coverage for Plan-2 scope: AgentSpawner interface + tmux managed/attach ✔ (T3); spawn mechanics incl. wrapper + env ✔ (T4, T6); agent integration — preamble all providers, Claude Stop hook, Codex notify (verify-at-implementation), Junie/Kimi wrapper-only ✔ (T5); `erbrus start` incl. overrides, `--fg`, `--no-create`, auto-configure ✔ (T9); reconcile on restart ✔ (T8); handoff/`origin_message_id` + origin system note ✔ (T6/T7); per-repo config relocation ✔ (T1); carry-over hardening ✔ (T2). Web UI spawn dialog: Plan 3.
- Type consistency: `spawn.RunSpec`/`Handle`/`Spawner` used identically in T3/T6/T7/T8; `runJSON`/`fgJSON` mirrored by `client.RunResult` (T9); `store.StartRun` introduced in T6 and used in T8's test; `config.LoadRepo` 3-value signature updated at its single Plan-1 call site in T1.
- Known simplifications, intentional: run tokens never expire early (the exit report is the last authorized call and FinishRun does not invalidate the token — a finished run's token merely stops mattering); `--fg` leaves run status `starting` until the wrap flow reports; codex hook degrades to wrapper-only if notify syntax cannot be verified locally; `set-option remain-on-exit` failure is non-fatal.
- Parked for Plan 3: artifact download endpoint (`GET /api/artifacts/{id}`); UI for spawn dialog incl. target project/channel selectors over the same `POST /api/runs` (it already accepts any channel_id, so cross-project spawn works via API today); rendering timestamps in local time.
