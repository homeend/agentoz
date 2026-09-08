# Agents Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `erbrus agents setup` discovers installed agent CLIs, installs the versioned erbrus skill into each, seeds provider defaults; agents can read their own screens, test rules and save providers through the erbrus CLI; providers without a `{prompt}` get the prompt pasted.

**Architecture:** Two leaf packages (`agentskill`: embedded skill; `agents`: registry + detection + install) feed a CLI command. Config gains `SetProvider` (yaml.v3 node editing) and `prompt: arg|paste`. The server exposes three small JSON routes the CLI wraps. Spawn gains a paste-delivery goroutine driven by the existing screen classifier.

**Tech Stack:** Go 1.26, chi, yaml.v3, existing `internal/screen` classifier, no new dependencies.

**Spec:** /mnt/t/others/erbrus/.claude/worktrees/agents-setup/docs/superpowers/specs/2026-09-08-agents-setup-design.md

## Global Constraints

- Work in `/mnt/t/others/erbrus/.claude/worktrees/agents-setup` on branch `worktree-agents-setup`; commit per task with `/usr/bin/git`; never push; main untouched until the user asks.
- No new Go dependencies. `go test ./...` must pass without a tmux server and must never touch the developer's real home: every test that reads or writes skill files or config uses `t.TempDir()`; `agentsHomeDir` and `CODEX_HOME`/`ERBRUS_CONFIG` are pinned in tests.
- Registry entries: exactly claude-code, codex, kimi, junie, antigravity, with the skill paths and provider defaults from the spec table; no `default_model` in the registry.
- Skill marker `<!-- erbrus:erbrus:vN -->`; skill files are erbrus-owned and overwritten whole.
- Worktree shell guard: no heredocs, no `$VAR` in front of a path, no `git`/`github` substring in Bash except plain `/usr/bin/git` commands; write files with the Write tool; spell paths out.
- Behavior change to state in commit messages: providers whose command lacks `{prompt}` now get the prompt by paste.

---

### Task 1: `internal/agentskill` — the embedded skill

**Files:**
- Create: `internal/agentskill/erbrus.md`, `internal/agentskill/agentskill.go`
- Test: `internal/agentskill/agentskill_test.go`, plus a drift guard in `internal/integrate/integrate_test.go`

**Interfaces:**
- Produces: `const Version = 1`, `func Body() string`, `func SkillFile() string`, `func HasMarker([]byte) bool`, `func InstalledVersion([]byte) int`, `const Name = "erbrus"`, `const Description`.

- [ ] **Step 1: Write the failing tests**

`internal/agentskill/agentskill_test.go`:

```go
package agentskill

import (
	"fmt"
	"strings"
	"testing"
)

func TestSkillFileShape(t *testing.T) {
	f := SkillFile()
	for _, want := range []string{
		"---\nname: erbrus\n", "description: Use when", "---\n\n",
		fmt.Sprintf("<!-- erbrus:erbrus:v%d -->", Version),
		"erbrus msg send --report --md", "<<'EOF'", "erbrus provider set", "erbrus screen capture --run", "erbrus screen test --provider",
		"screen_working", "screen_waiting", "screen_question", "prompt: paste", "{prompt}", "{model}", "ERBRUS_RUN_ID",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("skill missing %q", want)
		}
	}
	if !strings.HasPrefix(f, "---\n") {
		t.Error("frontmatter must open the file")
	}
	if strings.Count(f, "\n---\n") != 1 {
		t.Errorf("exactly one frontmatter close expected:\n%s", f[:200])
	}
}

func TestMarkerParsing(t *testing.T) {
	if !HasMarker([]byte(SkillFile())) || InstalledVersion([]byte(SkillFile())) != Version {
		t.Fatal("rendered file must carry the current marker")
	}
	old := []byte("---\nname: erbrus\n---\n<!-- erbrus:erbrus:v0 -->\nold")
	if !HasMarker(old) || InstalledVersion(old) != 0 {
		t.Errorf("v0 marker: has=%v v=%d", HasMarker(old), InstalledVersion(old))
	}
	if HasMarker([]byte("---\nname: erbrus\n---\nsomebody else's file")) {
		t.Error("foreign file must not count as ours")
	}
	if InstalledVersion(nil) != -1 {
		t.Error("no marker must read as -1")
	}
}
```

Append to `internal/integrate/integrate_test.go`:

```go
// The skill's "working as an erbrus agent" part and the spawn preamble
// teach the same protocol; keep the load-bearing phrases in both.
func TestPreambleAndSkillAgree(t *testing.T) {
	p := Preamble("/abs/erbrus", "a", "general")
	s := agentskill.Body()
	for _, want := range []string{"msg send --report --md", "<<'EOF'", "--file", "Do not start any work on your own"} {
		if !strings.Contains(p, want) || !strings.Contains(s, want) {
			t.Errorf("%q must appear in both preamble and skill", want)
		}
	}
}
```
(add `"erbrus/internal/agentskill"` to that file's imports.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentskill/ ./internal/integrate/`
Expected: build failure (package missing).

- [ ] **Step 3: Write the skill body**

`internal/agentskill/erbrus.md` (no frontmatter, no marker — the Go code adds them):

````markdown
# erbrus

erbrus is a local server that runs AI coding agents in tmux windows and
gives each one a chat channel. Two situations bring you here.

## 1. You are running as an erbrus agent

erbrus started you with a preamble naming your agent and channel. Your
environment has `ERBRUS_URL`, `ERBRUS_TOKEN`, `ERBRUS_CHANNEL` and
`ERBRUS_RUN_ID`; the `erbrus` command in the preamble is an absolute path
and already knows them.

- Do not start any work on your own until you are explicitly asked to do
  something. Humans and other agents post tasks into your channel.
- Report progress and results to the channel. Flags go before the text,
  and the text goes in SINGLE quotes:
  ```
  erbrus msg send --report --md 'what you did'
  ```
- Text with quotes or backticks goes on stdin instead:
  ```
  erbrus msg send --report --md - <<'EOF'
  what you did, including "quotes" and `code`, untouched
  EOF
  ```
- Attach a produced document with `--file /abs/path/to/report.md`.
- A message forwarded to you from another channel says so in its header
  and ends with the exact reply command; reply to that ORIGIN channel
  with `--channel <id>`, not to your own.
- Post a report as your FINAL step — the next agent picks up from it.
- Chat messages typed into your terminal come from the erbrus composer;
  the sender only sees the channel, so answer with `erbrus msg send`,
  not only on screen.

## 2. You are configuring a CLI as an erbrus provider

A provider is an entry under `providers:` in erbrus's global
`config.yaml` (`~/.config/erbrus/config.yaml`). Keys:

| key | meaning |
|---|---|
| `command` | shell template. Placeholders: `{model}`, `{args}`, `{prompt}`. erbrus shell-quotes `{model}` and `{prompt}`; an empty placeholder is dropped together with the flag before it (`--model {model}` vanishes when no model is set). |
| `default_model` | used when a run names no model; leave unset to let the CLI use its own default |
| `prompt` | `arg` (default when the command contains `{prompt}`) or `paste`: erbrus starts the CLI without the prompt and pastes it into the terminal once the input box is visible. Needed for CLIs whose interactive mode takes no initial prompt (Kimi Code). |
| `screen_working`, `screen_waiting`, `screen_question` | RE2 regex lists that read the CLI's screen; see below |

### How screen classification works

Every few seconds erbrus captures the tmux window, strips colors, trims
each line, drops empty lines, keeps the last 15 lines and joins them with
newlines. Each pattern is compiled with `(?m)` and may span lines with
`\n`. Order: if any *working* pattern matches → working; else any
*waiting* pattern → waiting (idle, turn finished); else any *question*
pattern → question (a dialog needs a human); else unknown, which changes
nothing. Any non-empty list replaces ALL built-in rules for that provider,
so give all three when you override.

Good rules are structural, not textual: the spinner line with its timer
for working; "the prompt glyph directly under a rule line" for waiting;
the dialog chrome (`Esc to cancel`, `❯ 1.`) for question. Scrollback text
must not be able to fake a state.

### The workflow

1. Read the CLI's `--help`. Find: the flag for the model, the flag or
   positional argument for an initial prompt in interactive mode (if there
   is none, use `prompt: paste`), and the auto-approve flag erbrus agents
   need (`--yolo`, `--dangerously-skip-permissions`, …).
2. Save a first version:
   ```
   erbrus provider set <name> --command '<cli> --model {model} {args} "{prompt}"' --prompt arg
   ```
   `erbrus provider show <name>` prints what is saved and whether the
   rules are built-in.
3. Get a running instance of the CLI under erbrus: spawn it from the web
   UI, or, if you ARE that CLI, use your own run id from `ERBRUS_RUN_ID`.
4. Look at the screens:
   ```
   erbrus screen capture --run <id> --lines 40
   ```
   Capture it idle at its input box, while it is busy (if you are the
   agent, run the capture from a tool call while your turn is in
   progress — the spinner is on screen then), and on a dialog if you can
   provoke one safely (a trust prompt, a permission question).
5. Write the three lists and test them without saving:
   ```
   erbrus screen test --provider <name> --run <id> --working '<re>' --waiting '<re>' --question '<re>'
   ```
   It prints the resulting state and marks which line matched which kind.
   Flags repeat for more patterns.
6. Save: `erbrus provider set <name> --working '<re>' --waiting '<re>' --question '<re>'`
   (repeat flags; `--clear-rules` returns to built-ins). The running
   erbrus applies the change immediately.
7. Report what you configured and what you could not verify.

### Worked examples (built in)

claude-code — working `\S+… \(\d+`, `⎿\s+Running…`; waiting `^─{8,}\n❯`
(input box under a rule); question `^❯ \d+\.`, `Esc to cancel`,
`Esc to go back`, `\(y/n\)`, `\[Y/n\]`, `Do you want to proceed`.

codex — working `Working \(\d+`, `(?i)esc to interrupt`; waiting
`^›[^\n]*\n[^\n]*· /` (input line above the model/cwd status line);
question `\(y/n\)`, `\[Y/n\]`, `Press Enter`, `^\s*[›>] \d+\.`.

kimi (registry default, working rule unverified) — command
`kimi --yolo --model {model} {args}`, `prompt: paste`; working
`^[🌑🌒🌓🌔🌕🌖🌗🌘] `, `Retrying \(\d+/\d+\)`; waiting `^│ >[^\n]*\n╰`;
question `Trust this folder\?`, `Enter select`, `Esc exit`.

antigravity — `agy --dangerously-skip-permissions --model "{model}" {args} -i "{prompt}"`.
````

- [ ] **Step 4: Write the Go side**

`internal/agentskill/agentskill.go`:

```go
// Package agentskill carries the "erbrus" skill: how to work as an erbrus
// agent and how to configure a CLI as an erbrus provider. The content is
// compiled in; installed copies are derived and overwritten whole.
package agentskill

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
)

//go:embed erbrus.md
var body string

// Version is bumped whenever erbrus.md changes; installed copies carry it
// so `erbrus agents setup` can tell new / outdated / up to date apart.
const Version = 1

const (
	Name        = "erbrus"
	Description = "Use when running as an erbrus agent (report to your channel with erbrus msg send, reply to forwards) or when configuring an AI coding CLI as an erbrus provider (command template, prompt delivery, screen rules)."
)

// Body is the canonical markdown, no frontmatter, no marker.
func Body() string { return body }

func marker() string { return fmt.Sprintf("<!-- erbrus:erbrus:v%d -->", Version) }

// SkillFile renders the SKILL.md every supported CLI reads: frontmatter,
// version marker, body.
func SkillFile() string {
	return "---\nname: " + Name + "\ndescription: " + Description + "\n---\n\n" + marker() + "\n\n" + body
}

var versionRe = regexp.MustCompile(`erbrus:erbrus:v(\d+)`)

// HasMarker reports whether content is an erbrus-installed skill (any version).
func HasMarker(content []byte) bool { return versionRe.Match(content) }

// InstalledVersion returns the stamped version, or -1 without a marker.
func InstalledVersion(content []byte) int {
	m := versionRe.FindSubmatch(content)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return -1
	}
	return n
}
```

- [ ] **Step 5: Run tests, commit**

Run: `go test ./internal/agentskill/ ./internal/integrate/ -v 2>&1 | tail -12`
Expected: PASS.

```bash
/usr/bin/git add internal/agentskill internal/integrate/integrate_test.go
/usr/bin/git commit -m "feat(agentskill): embedded, versioned erbrus skill"
```

---

### Task 2: `internal/agents` — registry, detection, install

**Files:**
- Create: `internal/agents/agents.go`
- Test: `internal/agents/agents_test.go`

**Interfaces:**
- Consumes: `agentskill.SkillFile/HasMarker/InstalledVersion/Version`, `config.Provider`, `provider.Registry.Render` (test only).
- Produces:
  ```go
  type Agent struct{ ID, Label, Binary, Home, Skill string; Provider config.Provider; Note string }
  func Builtins() []Agent
  type Status int; const ( StatusNew Status = iota; StatusOutdated; StatusUpToDate ); func (Status) String() string; func (Status) Checked() bool
  type Detection struct{ Agent Agent; BinaryPath, SkillPath string; Status Status; Configured bool }
  func Detect(homeDir string, lookPath func(string) (string, error), cfg config.Global) []Detection
  func Install(d Detection) error
  ```

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/agents/` → build failure.

- [ ] **Step 3: Implement**

```go
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
```

Note: `TestDetect` expects `SkillPath` for an agent detected only through its binary with `homeDir == ""` to be `""`; that case only arises in the empty-home test where nothing is detected. With a real home the path always resolves.

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/agents/ -v 2>&1 | tail -8` → PASS (the `Prompt` field does not exist yet in `config.Provider`; if Task 3 has not run, add the field first — see Task 3 Step 3, it is one line).

```bash
/usr/bin/git add internal/agents
/usr/bin/git commit -m "feat(agents): registry of supported agent CLIs, detection, skill install"
```

---

### Task 3: config — `Provider.Prompt` and `SetProvider`

**Files:**
- Modify: `internal/config/config.go` (field), `internal/config/screenrules.go` (generalize)
- Test: `internal/config/screenrules_test.go` (append)

**Interfaces:**
- Produces:
  ```go
  type ProviderPatch struct { Command, DefaultModel, Prompt *string; Working, Waiting, Question *[]string }
  func SetProvider(doc []byte, name string, p ProviderPatch) ([]byte, error)  // creates providers:/entry when missing
  func SetProviderScreenRules(doc []byte, provider string, working, waiting, question []string) ([]byte, error) // unchanged signature, now via SetProvider but still errors when the provider is missing
  func Str(s string) *string; func List(l []string) *[]string  // patch helpers
  ```

- [ ] **Step 1: Write the failing tests** (append to `screenrules_test.go`)

```go
func TestSetProviderCreatesAndUpdates(t *testing.T) {
	doc := []byte("# mine\nport: 7420\n")
	out, err := SetProvider(doc, "kimi", ProviderPatch{Command: Str(`kimi --yolo --model {model} {args}`), Prompt: Str("paste"),
		Waiting: List([]string{`^│ >[^\n]*\n╰`})})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# mine", "port: 7420", "providers:", "  kimi:", "    command: kimi --yolo --model {model} {args}", "    prompt: paste", `screen_waiting: ['^│ >[^\n]*\n╰']`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	g, err := parseGlobal(out)
	if err != nil || g.Providers["kimi"].Prompt != "paste" || len(g.Providers["kimi"].ScreenWaiting) != 1 {
		t.Fatalf("round trip: %+v %v", g.Providers["kimi"], err)
	}
	// Update one field, keep the rest; clear a list with an empty one.
	out2, err := SetProvider(out, "kimi", ProviderPatch{DefaultModel: Str("kimi-code/k3"), Waiting: List(nil)})
	if err != nil {
		t.Fatal(err)
	}
	g, _ = parseGlobal(out2)
	p := g.Providers["kimi"]
	if p.Command == "" || p.Prompt != "paste" || p.DefaultModel != "kimi-code/k3" || len(p.ScreenWaiting) != 0 {
		t.Fatalf("update: %+v", p)
	}
	if strings.Contains(string(out2), "screen_waiting") || !strings.Contains(string(out2), "# mine") {
		t.Fatalf("cleared key or comment wrong:\n%s", out2)
	}
}

func TestSetProviderScreenRulesStillRequiresProvider(t *testing.T) {
	if _, err := SetProviderScreenRules([]byte("providers:\n  x:\n    command: x\n"), "nope", nil, nil, nil); err == nil {
		t.Fatal("unknown provider must error")
	}
}
```

`parseGlobal` is whatever the config package already uses to unmarshal a document into `Global` (check `LoadGlobal`; if it only reads files, write the bytes to a temp file and call `LoadGlobal`).

- [ ] **Step 2: Run to verify failure** — undefined `SetProvider`, `ProviderPatch`.

- [ ] **Step 3: Implement**

`config.go`: add to `Provider` after `DefaultModel`:
```go
	// Prompt is how the prompt reaches the CLI: "arg" (through {prompt}
	// in Command, the default when the template has it) or "paste" (typed
	// into the terminal once the input box is up — for CLIs whose
	// interactive mode takes no initial prompt).
	Prompt string `yaml:"prompt"`
```

`screenrules.go`: add

```go
// ProviderPatch is a partial provider update: nil means keep, an empty
// list clears the key.
type ProviderPatch struct {
	Command, DefaultModel, Prompt *string
	Working, Waiting, Question    *[]string
}

func Str(s string) *string      { return &s }
func List(l []string) *[]string { return &l }

// SetProvider rewrites one provider inside config.yaml, keeping every
// other key and comment (yaml.v3 node editing). Missing providers: or the
// entry itself are created, so a fresh config works too.
func SetProvider(doc []byte, name string, p ProviderPatch) ([]byte, error) {
	root, top, err := parseDoc(doc)
	if err != nil {
		return nil, err
	}
	providers := mapValue(top, "providers")
	if providers == nil {
		providers = &yaml.Node{Kind: yaml.MappingNode}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "providers"}, providers)
	} else if providers.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: providers is not a mapping")
	}
	entry := mapValue(providers, name)
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode}
		providers.Content = append(providers.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, entry)
	} else if entry.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: provider %q is not a mapping", name)
	}
	for _, kv := range []struct {
		key string
		val *string
	}{{"command", p.Command}, {"default_model", p.DefaultModel}, {"prompt", p.Prompt}} {
		if kv.val != nil {
			setScalar(entry, kv.key, *kv.val)
		}
	}
	for _, kv := range []struct {
		key  string
		list *[]string
	}{{"screen_working", p.Working}, {"screen_waiting", p.Waiting}, {"screen_question", p.Question}} {
		if kv.list != nil {
			setSeq(entry, kv.key, *kv.list)
		}
	}
	return encodeDoc(root)
}

// setScalar sets key to a plain scalar (removes it when v is empty).
func setScalar(m *yaml.Node, key, v string) {
	idx := -1
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			idx = i
			break
		}
	}
	if v == "" {
		if idx >= 0 {
			m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
		}
		return
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	if idx >= 0 {
		m.Content[idx+1] = n
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, n)
}
```

Refactor the existing `SetProviderScreenRules` into: parse via a new `parseDoc(doc) (root *yaml.Node, top *yaml.Node, err error)` (the first 12 lines of the current function), keep its "provider must exist" check, then call `SetProvider(doc, provider, ProviderPatch{Working: List(working), Waiting: List(waiting), Question: List(question)})`. Move the encoder tail into `encodeDoc(root *yaml.Node) ([]byte, error)`.

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/config/ -v 2>&1 | tail -8` → PASS, and `go test ./internal/server/ -run TestSettingsScreenRules` still PASS.

```bash
/usr/bin/git add internal/config
/usr/bin/git commit -m "feat(config): Provider.prompt (arg|paste) and SetProvider — create/update a provider in config.yaml keeping comments"
```

---

### Task 4: `provider.Render` quotes the model, drops on raw emptiness; `validModel` relaxed

**Files:**
- Modify: `internal/provider/provider.go`, `internal/server/runs.go` (`validModel`)
- Test: `internal/provider/provider_test.go`, `internal/server/runs_test.go` (whichever test rejects `$(` in a model)

- [ ] **Step 1: Write the failing tests** (append to `provider_test.go`)

```go
func TestRenderQuotesModel(t *testing.T) {
	r := Registry{"agy": {Command: `agy --model "{model}" {args} -i "{prompt}"`}, "k": {Command: `k --model {model} "{prompt}"`}}
	got, _ := r.Render("agy", "Gemini 3.6 Flash (High)", "", "hi")
	if got != `agy --model 'Gemini 3.6 Flash (High)' -i 'hi'` {
		t.Errorf("got %q", got)
	}
	got, _ = r.Render("k", "sonnet", "", "hi")
	if got != `k --model 'sonnet' 'hi'` {
		t.Errorf("plain model must be quoted too: %q", got)
	}
}

func TestRenderEmptyPromptDropsPlaceholder(t *testing.T) {
	r := Registry{"k": {Command: `k --model {model} {args} "{prompt}"`}}
	got, _ := r.Render("k", "m", "", "")
	if got != `k --model 'm'` {
		t.Errorf("empty prompt must vanish, got %q", got)
	}
	r2 := Registry{"p": {Command: `p -i "{prompt}"`}}
	if got, _ := r2.Render("p", "", "", ""); got != `p` {
		t.Errorf("empty prompt must drop its flag, got %q", got)
	}
}
```

Adjust existing expectations: `TestRenderDefaultModel` wants `--model 'fable-5'`; any other test asserting `--model x` bare now expects `'x'`. In the server package, change the model-validation test to accept `Claude Opus 4.6 (Thinking)` and to reject a model containing `"\n"`.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/provider/` FAIL on the new tests.

- [ ] **Step 3: Implement**

In `Render`: build `raw := map[string]string{"model": model, "args": args, "prompt": prompt}`; when emitting a placeholder token, `v := raw[key]`; if `v == ""` do the existing drop; else `text := v`, and for `key == "model" || key == "prompt"` set `text = ShellQuote(v)`. Remove the pre-quoting from `vals`. Update the comment: "{model} and {prompt} are shell-quoted; the template's own quotes around them are replaced by ours".

`runs.go`:
```go
// validModel: the model is single-quoted by Render, so anything but a
// newline or NUL is safe (agy model names carry spaces and parentheses).
func validModel(model string) bool { return !strings.ContainsAny(model, "\n\x00") }
```
Remove `modelRe` if now unused.

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/provider/ ./internal/server/ 2>&1 | tail -4` → PASS.

```bash
/usr/bin/git add internal/provider internal/server/runs.go internal/server/runs_test.go
/usr/bin/git commit -m "feat(provider): shell-quote {model}; drop empty placeholders on the raw value (empty prompt no longer renders as '')"
```

---

### Task 5: Server API — run screen, screen test, provider get/put

**Files:**
- Create: `internal/server/api_providers.go`
- Modify: `internal/server/server.go` (routes)
- Test: `internal/server/api_providers_test.go`

**Interfaces:**
- Consumes: `screen.*`, `config.SetProvider`, `s.setRules`, `writeFileAtomic`, `s.configPath`.
- Produces routes: `GET /api/runs/{id}/screen?lines=N`, `POST /api/providers/{name}/screen-test`, `GET /api/providers/{name}`, `PUT /api/providers/{name}`. JSON shapes:
  ```go
  type screenJSON struct { State string `json:"state"`; Lines []string `json:"lines"`; Options []screen.Option `json:"options,omitempty"`; Cols, Rows int; Dead bool }
  type screenTestReq struct { Run int64 `json:"run"`; Working, Waiting, Question []string }
  type screenTestJSON struct { State string `json:"state"`; Lines []testLineJSON `json:"lines"` }  // testLineJSON{Text, Match string}
  type providerJSON struct { Name, Command, DefaultModel, Prompt string; ScreenWorking, ScreenWaiting, ScreenQuestion []string; RulesSource string }
  type providerPatchJSON struct { Command, DefaultModel, Prompt *string; ScreenWorking, ScreenWaiting, ScreenQuestion *[]string }
  ```
  (json tags: snake_case as in the spec.)

- [ ] **Step 1: Write the failing tests**

```go
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

func doJSON(t *testing.T, method, url string, in any) (*http.Response, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	if in != nil {
		json.NewEncoder(&body).Encode(in)
	}
	req, _ := http.NewRequest(method, url, &body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestAPIRunScreen(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	var raw strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&raw, "line %d\n", i)
	}
	raw.WriteString("Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n")
	fs.setScreen("s:5", spawn.Screen{Raw: raw.String(), Cols: 100, Rows: 40, Activity: time.Now()})

	resp, out := doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen", ts.URL, run.ID), nil)
	if resp.StatusCode != 200 || out["state"] != "question" || len(out["lines"].([]any)) != 15 || out["cols"].(float64) != 100 {
		t.Fatalf("screen: %d %v", resp.StatusCode, out)
	}
	if opts := out["options"].([]any); len(opts) != 2 || opts[0].(map[string]any)["label"] != "Yes" {
		t.Fatalf("options: %v", out["options"])
	}
	_, out = doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen?lines=25", ts.URL, run.ID), nil)
	if n := len(out["lines"].([]any)); n != 25 {
		t.Fatalf("lines=25 gave %d", n)
	}
	_, out = doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen?lines=9999", ts.URL, run.ID), nil)
	if n := len(out["lines"].([]any)); n != 34 { // capped at 200, screen has 34 non-empty lines
		t.Fatalf("lines cap gave %d", n)
	}
	st.FinishRun(run.ID, "done", 0)
	st.SetRunTarget(run.ID, "") // if no such helper exists, create a run without TmuxTarget instead
	resp, _ = doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen", ts.URL, run.ID), nil)
	if resp.StatusCode != 404 {
		t.Fatalf("no window: %d", resp.StatusCode)
	}
}

func TestAPIScreenTestAndProviderPut(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("# mine\nport: 7420\n"), 0o644)
	testSrv.SetConfigPath(cfgPath)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	fs.setScreen("s:5", spawn.Screen{Raw: "please CONFIRM\n", Activity: time.Now()})

	// Test with unsaved rules for a provider that does not exist yet.
	resp, out := doJSON(t, "POST", ts.URL+"/api/providers/newcli/screen-test", map[string]any{"run": run.ID, "question": []string{"CONFIRM"}})
	if resp.StatusCode != 200 || out["state"] != "question" {
		t.Fatalf("screen-test: %d %v", resp.StatusCode, out)
	}
	if l := out["lines"].([]any)[0].(map[string]any); l["match"] != "question" {
		t.Fatalf("line match: %v", l)
	}
	resp, _ = doJSON(t, "POST", ts.URL+"/api/providers/newcli/screen-test", map[string]any{"run": run.ID, "working": []string{"("}})
	if resp.StatusCode != 422 {
		t.Fatalf("bad pattern status = %d", resp.StatusCode)
	}

	// PUT creates the provider, writes the file, applies live.
	resp, out = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"command": "newcli {args} \"{prompt}\"", "prompt": "arg", "screen_question": []string{"CONFIRM"}})
	if resp.StatusCode != 200 || out["command"] != "newcli {args} \"{prompt}\"" || out["rules_source"] != "config override" {
		t.Fatalf("put: %d %v", resp.StatusCode, out)
	}
	doc, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(doc), "# mine") || !strings.Contains(string(doc), "  newcli:") || !strings.Contains(string(doc), "screen_question: ['CONFIRM']") {
		t.Fatalf("config:\n%s", doc)
	}
	if got := screen.Classify(testSrv.rulesFor("newcli"), []string{"CONFIRM"}); got != screen.Question {
		t.Fatalf("rules not live: %q", got)
	}
	if _, ok := testSrv.cfg.Providers["newcli"]; !ok {
		t.Fatal("provider not in the running config")
	}
	// GET shows it; a partial PUT keeps other fields.
	_, out = doJSON(t, "GET", ts.URL+"/api/providers/newcli", nil)
	if out["prompt"] != "arg" || out["command"] == "" {
		t.Fatalf("get: %v", out)
	}
	_, out = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"default_model": "m1"})
	if out["command"] == "" || out["default_model"] != "m1" {
		t.Fatalf("partial put: %v", out)
	}
	resp, _ = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"screen_working": []string{"("}})
	if resp.StatusCode != 422 {
		t.Fatalf("bad pattern put = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, "GET", ts.URL+"/api/providers/none", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown provider get = %d", resp.StatusCode)
	}
}
```

If `st.SetRunTarget` does not exist, replace that part with a second run created via `st.CreateRun` with an empty `TmuxTarget` and assert 404 on it.

- [ ] **Step 2: Run to verify failure** — 404s from unrouted paths.

- [ ] **Step 3: Implement** `internal/server/api_providers.go`

```go
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

const maxScreenLines = 200

type screenJSON struct {
	State   string          `json:"state"`
	Lines   []string        `json:"lines"`
	Options []screen.Option `json:"options,omitempty"`
	Cols    int             `json:"cols"`
	Rows    int             `json:"rows"`
	Dead    bool            `json:"dead"`
}

// handleAPIRunScreen: the stripped tail of a run's screen plus its
// classification — what an agent needs to write screen rules for itself.
func (s *Server) handleAPIRunScreen(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || run.TmuxTarget == "" {
		httpError(w, http.StatusNotFound, "run has no tmux window")
		return
	}
	if s.spawner == nil {
		httpError(w, http.StatusServiceUnavailable, "no spawner configured")
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 {
		n = 15
	}
	if n > maxScreenLines {
		n = maxScreenLines
	}
	sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
	if err != nil {
		httpError(w, http.StatusNotFound, "screen unavailable: "+err.Error())
		return
	}
	text := screen.Strip(sc.Raw)
	tail := screen.Tail(text, 15)
	st := screen.Classify(s.rulesFor(run.Provider), tail)
	out := screenJSON{State: string(st), Lines: screen.Tail(text, n), Cols: sc.Cols, Rows: sc.Rows, Dead: sc.Dead}
	if st == screen.Question {
		out.Options = screen.Options(tail)
	}
	writeJSON(w, http.StatusOK, out)
}

type screenTestReq struct {
	Run      int64    `json:"run"`
	Working  []string `json:"working"`
	Waiting  []string `json:"waiting"`
	Question []string `json:"question"`
}

type testLineJSON struct {
	Text  string `json:"text"`
	Match string `json:"match"`
}

// handleAPIScreenTest classifies a run's screen with UNSAVED patterns.
// The provider name need not exist yet: agents test before they save.
func (s *Server) handleAPIScreenTest(w http.ResponseWriter, r *http.Request) {
	var req screenTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	rules, err := screen.Compile(req.Working, req.Waiting, req.Question)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	run, ok, _ := s.st.RunByID(req.Run)
	if !ok || run.TmuxTarget == "" {
		httpError(w, http.StatusNotFound, "run has no tmux window")
		return
	}
	if s.spawner == nil {
		httpError(w, http.StatusServiceUnavailable, "no spawner configured")
		return
	}
	sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
	if err != nil {
		httpError(w, http.StatusNotFound, "screen unavailable: "+err.Error())
		return
	}
	lines := screen.Tail(screen.Strip(sc.Raw), 15)
	out := struct {
		State string         `json:"state"`
		Lines []testLineJSON `json:"lines"`
	}{State: string(screen.Classify(rules, lines))}
	for _, l := range lines {
		out.Lines = append(out.Lines, testLineJSON{Text: l, Match: screen.MatchKind(rules, l)})
	}
	writeJSON(w, http.StatusOK, out)
}

type providerJSON struct {
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	DefaultModel   string   `json:"default_model"`
	Prompt         string   `json:"prompt"`
	ScreenWorking  []string `json:"screen_working"`
	ScreenWaiting  []string `json:"screen_waiting"`
	ScreenQuestion []string `json:"screen_question"`
	RulesSource    string   `json:"rules_source"`
}

func (s *Server) providerJSON(name string, p config.Provider) providerJSON {
	src := "config override"
	if len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) == 0 {
		src = "built-in (generic)"
		if screen.HasDefaults(name) {
			src = "built-in (" + name + ")"
		}
	}
	return providerJSON{Name: name, Command: p.Command, DefaultModel: p.DefaultModel, Prompt: p.Prompt,
		ScreenWorking: p.ScreenWorking, ScreenWaiting: p.ScreenWaiting, ScreenQuestion: p.ScreenQuestion, RulesSource: src}
}

func (s *Server) handleAPIGetProvider(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, ok := s.cfg.Providers[name]
	if !ok {
		httpError(w, http.StatusNotFound, "unknown provider")
		return
	}
	writeJSON(w, http.StatusOK, s.providerJSON(name, p))
}

type providerPatchJSON struct {
	Command        *string   `json:"command"`
	DefaultModel   *string   `json:"default_model"`
	Prompt         *string   `json:"prompt"`
	ScreenWorking  *[]string `json:"screen_working"`
	ScreenWaiting  *[]string `json:"screen_waiting"`
	ScreenQuestion *[]string `json:"screen_question"`
}

// handleAPIPutProvider creates or updates a provider: validates, rewrites
// config.yaml (comments kept), then applies to the running server.
func (s *Server) handleAPIPutProvider(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req providerPatchJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if req.Prompt != nil && *req.Prompt != "" && *req.Prompt != "arg" && *req.Prompt != "paste" {
		httpError(w, http.StatusUnprocessableEntity, `prompt must be "arg" or "paste"`)
		return
	}
	cur := s.cfg.Providers[name] // zero value when new
	next := cur
	if req.Command != nil {
		next.Command = *req.Command
	}
	if req.DefaultModel != nil {
		next.DefaultModel = *req.DefaultModel
	}
	if req.Prompt != nil {
		next.Prompt = *req.Prompt
	}
	if req.ScreenWorking != nil {
		next.ScreenWorking = *req.ScreenWorking
	}
	if req.ScreenWaiting != nil {
		next.ScreenWaiting = *req.ScreenWaiting
	}
	if req.ScreenQuestion != nil {
		next.ScreenQuestion = *req.ScreenQuestion
	}
	if next.Command == "" {
		httpError(w, http.StatusUnprocessableEntity, "command is required")
		return
	}
	rules, err := screen.Compile(next.ScreenWorking, next.ScreenWaiting, next.ScreenQuestion)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if s.configPath == "" {
		httpError(w, http.StatusBadRequest, "global config path not configured")
		return
	}
	doc, err := os.ReadFile(s.configPath)
	if err != nil && !os.IsNotExist(err) {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := config.SetProvider(doc, name, config.ProviderPatch{
		Command: req.Command, DefaultModel: req.DefaultModel, Prompt: req.Prompt,
		Working: req.ScreenWorking, Waiting: req.ScreenWaiting, Question: req.ScreenQuestion,
	})
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := writeFileAtomic(s.configPath, out); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.cfg.Providers == nil {
		s.cfg.Providers = map[string]config.Provider{}
	}
	s.cfg.Providers[name] = next
	if len(next.ScreenWorking)+len(next.ScreenWaiting)+len(next.ScreenQuestion) == 0 {
		s.setRules(name, nil)
	} else {
		s.setRules(name, &rules)
	}
	writeJSON(w, http.StatusOK, s.providerJSON(name, next))
}
```

`s.cfg.Providers` is read by spawn handlers concurrently; if `s.cfg` is not already guarded, guard the map with `s.rulesMu` (write under `Lock`, reads in `spawnCore`/settings under `RLock`) — check how `handleUISettingsScreen` writes it today and follow the same discipline.

Routes in `server.go` inside `r.Route("/api", …)` after `handleChannelRuns`:
```go
		r.Get("/runs/{id}/screen", s.handleAPIRunScreen)
		r.Post("/providers/{name}/screen-test", s.handleAPIScreenTest)
		r.Get("/providers/{name}", s.handleAPIGetProvider)
		r.Put("/providers/{name}", s.handleAPIPutProvider)
```

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/server/ -race 2>&1 | tail -3` → PASS.

```bash
/usr/bin/git add internal/server/api_providers.go internal/server/api_providers_test.go internal/server/server.go
/usr/bin/git commit -m "feat(api): run screen, screen-test and provider get/put for self-configuring agents"
```

---

### Task 6: Client + CLI `provider set|show`, `screen capture|test`

**Files:**
- Modify: `internal/client/client.go`
- Create: `internal/cli/provider.go`, `internal/cli/screen.go`
- Modify: `internal/cli/cli.go` (dispatch + usage)
- Test: `internal/cli/provider_test.go`

**Interfaces:**
- Produces in `client`: `type Provider struct{…json as providerJSON…}`, `func (c *Client) GetProvider(name string) (Provider, error)`, `func (c *Client) PutProvider(name string, patch map[string]any) (Provider, error)`, `type Screen struct{State string; Lines []string; Options []struct{Key,Label string}; Cols,Rows int; Dead bool}`, `func (c *Client) RunScreen(runID int64, lines int) (Screen, error)`, `type ScreenTest struct{State string; Lines []struct{Text,Match string}}`, `func (c *Client) ScreenTest(provider string, runID int64, working, waiting, question []string) (ScreenTest, error)`.
- CLI: `erbrus provider set|show`, `erbrus screen capture|test` as in the spec.

- [ ] **Step 1: Write the failing tests** (`internal/cli/provider_test.go`)

```go
package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

func TestProviderSetShowAndScreen(t *testing.T) {
	url, ch := msgTestServer(t)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 7420\n"), 0o644)
	lastTestServer.SetConfigPath(cfgPath)
	fs := &cliFakeSpawner{raw: "hello\n❯\n"}
	lastTestServer.SetRuntime(fs, "/abs/erbrus", url)
	run, _ := lastTestStore.CreateRun(store.AgentRun{ChannelID: ch, Provider: "newcli", AgentName: "n", Workdir: "/w", Status: "running", Spawner: "tmux", TmuxTarget: "s:1"})
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_TOKEN", "")

	var out, errOut bytes.Buffer
	code := Run([]string{"provider", "set", "newcli", "--command", `newcli --model {model} {args} "{prompt}"`, "--prompt", "arg", "--waiting", `^❯\s*$`}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "saved provider newcli") {
		t.Fatalf("set: %d %s %s", code, out.String(), errOut.String())
	}
	if doc, _ := os.ReadFile(cfgPath); !strings.Contains(string(doc), "  newcli:") {
		t.Fatalf("config not written:\n%s", doc)
	}
	out.Reset()
	if code := Run([]string{"provider", "show", "newcli"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "command: newcli") || !strings.Contains(out.String(), "config override") {
		t.Fatalf("show: %d %s", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"screen", "capture", "--run", fmt.Sprint(run.ID)}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "state: waiting\n") || !strings.Contains(out.String(), "hello") {
		t.Fatalf("capture: %d %s %s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Run([]string{"screen", "test", "--provider", "x", "--run", fmt.Sprint(run.ID), "--question", "hello"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "state: question") || !strings.Contains(out.String(), "[question] hello") {
		t.Fatalf("test: %d %s %s", code, out.String(), errOut.String())
	}
	if code := Run([]string{"screen", "test", "--provider", "x", "--run", fmt.Sprint(run.ID), "--working", "("}, &out, &errOut); code == 0 {
		t.Fatal("bad pattern must fail")
	}
	out.Reset()
	if code := Run([]string{"provider", "set", "newcli", "--clear-rules"}, &out, &errOut); code != 0 {
		t.Fatalf("clear: %s", errOut.String())
	}
	out.Reset()
	Run([]string{"provider", "show", "newcli"}, &out, &errOut)
	if !strings.Contains(out.String(), "built-in") {
		t.Fatalf("rules not cleared: %s", out.String())
	}
}

// cliFakeSpawner: enough Spawner for screen capture in CLI tests.
type cliFakeSpawner struct{ raw string }

func (f *cliFakeSpawner) Spawn(spawn.RunSpec) (spawn.Handle, error)     { return "s:1", nil }
func (f *cliFakeSpawner) Stop(spawn.Handle) error                       { return nil }
func (f *cliFakeSpawner) Alive(spawn.Handle) (bool, error)              { return true, nil }
func (f *cliFakeSpawner) Send(spawn.Handle, string) error               { return nil }
func (f *cliFakeSpawner) SendKeys(spawn.Handle, string) error           { return nil }
func (f *cliFakeSpawner) Capture(spawn.Handle) (spawn.Screen, error) {
	return spawn.Screen{Raw: f.raw, Cols: 80, Rows: 24, Activity: time.Now()}, nil
}
```

`msgTestServer` must expose the server and store it built: add package vars `lastTestServer *server.Server` and `lastTestStore *store.Store` set inside `msgTestServer` (change `server.New(...)` into `srv := server.New(...); lastTestServer = srv; lastTestStore = st`).

- [ ] **Step 2: Run to verify failure** — "unknown command".

- [ ] **Step 3: Implement**

`client.go` additions:

```go
type Provider struct {
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	DefaultModel   string   `json:"default_model"`
	Prompt         string   `json:"prompt"`
	ScreenWorking  []string `json:"screen_working"`
	ScreenWaiting  []string `json:"screen_waiting"`
	ScreenQuestion []string `json:"screen_question"`
	RulesSource    string   `json:"rules_source"`
}

func (c *Client) GetProvider(name string) (Provider, error) {
	var p Provider
	return p, c.do("GET", "/api/providers/"+name, "", nil, &p)
}

// PutProvider sends a partial update; keys absent from patch are kept.
func (c *Client) PutProvider(name string, patch map[string]any) (Provider, error) {
	var p Provider
	b, _ := json.Marshal(patch)
	return p, c.do("PUT", "/api/providers/"+name, "application/json", bytes.NewReader(b), &p)
}

type ScreenOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type Screen struct {
	State   string         `json:"state"`
	Lines   []string       `json:"lines"`
	Options []ScreenOption `json:"options"`
	Cols    int            `json:"cols"`
	Rows    int            `json:"rows"`
	Dead    bool           `json:"dead"`
}

func (c *Client) RunScreen(runID int64, lines int) (Screen, error) {
	var s Screen
	return s, c.do("GET", fmt.Sprintf("/api/runs/%d/screen?lines=%d", runID, lines), "", nil, &s)
}

type ScreenTestLine struct {
	Text  string `json:"text"`
	Match string `json:"match"`
}

type ScreenTest struct {
	State string           `json:"state"`
	Lines []ScreenTestLine `json:"lines"`
}

func (c *Client) ScreenTest(provider string, runID int64, working, waiting, question []string) (ScreenTest, error) {
	var out ScreenTest
	return out, c.postJSON("/api/providers/"+provider+"/screen-test",
		map[string]any{"run": runID, "working": working, "waiting": waiting, "question": question}, &out)
}
```

(Check `do`'s error handling returns the server's `error` JSON text for non-2xx; the CLI prints it.)

`internal/cli/provider.go`:

```go
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"erbrus/internal/client"
)

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

const providerUsage = `usage: erbrus provider set <name> [--command S] [--default-model S] [--prompt arg|paste]
                           [--working RE]... [--waiting RE]... [--question RE]... [--clear-rules]
       erbrus provider show <name>
`

func runProvider(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, providerUsage)
		return 2
	}
	sub, name := args[0], args[1]
	c, _, _ := client.FromEnv()
	switch sub {
	case "show":
		p, err := c.GetProvider(name)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printProvider(stdout, p)
		return 0
	case "set":
		fs := flag.NewFlagSet("provider set", flag.ContinueOnError)
		fs.SetOutput(stderr)
		command := fs.String("command", "", "command template with {model} {args} {prompt}")
		model := fs.String("default-model", "", "default model (empty: the CLI's own default)")
		prompt := fs.String("prompt", "", "arg|paste")
		var working, waiting, question multiFlag
		fs.Var(&working, "working", "screen_working pattern (repeatable)")
		fs.Var(&waiting, "waiting", "screen_waiting pattern (repeatable)")
		fs.Var(&question, "question", "screen_question pattern (repeatable)")
		clear := fs.Bool("clear-rules", false, "remove all three lists (back to built-in rules)")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		patch := map[string]any{}
		visited := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
		if visited["command"] {
			patch["command"] = *command
		}
		if visited["default-model"] {
			patch["default_model"] = *model
		}
		if visited["prompt"] {
			patch["prompt"] = *prompt
		}
		if *clear {
			patch["screen_working"], patch["screen_waiting"], patch["screen_question"] = []string{}, []string{}, []string{}
		} else {
			if visited["working"] {
				patch["screen_working"] = []string(working)
			}
			if visited["waiting"] {
				patch["screen_waiting"] = []string(waiting)
			}
			if visited["question"] {
				patch["screen_question"] = []string(question)
			}
		}
		if len(patch) == 0 {
			fmt.Fprintln(stderr, "nothing to set")
			return 2
		}
		p, err := c.PutProvider(name, patch)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "saved provider %s (applied live)\n", name)
		printProvider(stdout, p)
		return 0
	}
	fmt.Fprint(stderr, providerUsage)
	return 2
}

func printProvider(w io.Writer, p client.Provider) {
	fmt.Fprintf(w, "command: %s\n", p.Command)
	if p.DefaultModel != "" {
		fmt.Fprintf(w, "default_model: %s\n", p.DefaultModel)
	}
	prompt := p.Prompt
	if prompt == "" {
		prompt = "arg"
		if !strings.Contains(p.Command, "{prompt}") {
			prompt = "paste (no {prompt} in command)"
		}
	}
	fmt.Fprintf(w, "prompt: %s\n", prompt)
	fmt.Fprintf(w, "screen rules: %s\n", p.RulesSource)
	for _, kv := range []struct {
		k string
		v []string
	}{{"working", p.ScreenWorking}, {"waiting", p.ScreenWaiting}, {"question", p.ScreenQuestion}} {
		for _, re := range kv.v {
			fmt.Fprintf(w, "  %s: %s\n", kv.k, re)
		}
	}
}
```

`internal/cli/screen.go`:

```go
package cli

import (
	"flag"
	"fmt"
	"io"

	"erbrus/internal/client"
)

const screenUsage = `usage: erbrus screen capture --run N [--lines M]
       erbrus screen test --provider P --run N [--working RE]... [--waiting RE]... [--question RE]...
`

func runScreen(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, screenUsage)
		return 2
	}
	c, _, _ := client.FromEnv()
	switch args[0] {
	case "capture":
		fs := flag.NewFlagSet("screen capture", flag.ContinueOnError)
		fs.SetOutput(stderr)
		run := fs.Int64("run", 0, "run id (default: ERBRUS_RUN_ID)")
		lines := fs.Int("lines", 15, "how many tail lines (max 200)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		id := runIDOr(*run)
		if id == 0 {
			fmt.Fprintln(stderr, "screen capture: --run required (or ERBRUS_RUN_ID)")
			return 2
		}
		sc, err := c.RunScreen(id, *lines)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		state := sc.State
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(stdout, "state: %s\n", state)
		if sc.Dead {
			fmt.Fprintln(stdout, "(process exited; final screen)")
		}
		for _, o := range sc.Options {
			fmt.Fprintf(stdout, "option %s: %s\n", o.Key, o.Label)
		}
		fmt.Fprintf(stdout, "--- %dx%d, last %d lines ---\n", sc.Cols, sc.Rows, len(sc.Lines))
		for _, l := range sc.Lines {
			fmt.Fprintln(stdout, l)
		}
		return 0
	case "test":
		fs := flag.NewFlagSet("screen test", flag.ContinueOnError)
		fs.SetOutput(stderr)
		provider := fs.String("provider", "", "provider name (need not exist yet)")
		run := fs.Int64("run", 0, "run id (default: ERBRUS_RUN_ID)")
		var working, waiting, question multiFlag
		fs.Var(&working, "working", "pattern (repeatable)")
		fs.Var(&waiting, "waiting", "pattern (repeatable)")
		fs.Var(&question, "question", "pattern (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		id := runIDOr(*run)
		if *provider == "" || id == 0 {
			fmt.Fprint(stderr, screenUsage)
			return 2
		}
		res, err := c.ScreenTest(*provider, id, working, waiting, question)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		state := res.State
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(stdout, "state: %s\n", state)
		for _, l := range res.Lines {
			tag := ""
			if l.Match != "" {
				tag = "[" + l.Match + "] "
			}
			fmt.Fprintf(stdout, "%s%s\n", tag, l.Text)
		}
		return 0
	}
	fmt.Fprint(stderr, screenUsage)
	return 2
}

// runIDOr returns flag when set, else ERBRUS_RUN_ID from the environment
// (an agent inspecting its own screen).
func runIDOr(flagVal int64) int64 {
	if flagVal != 0 {
		return flagVal
	}
	_, id, _ := client.FromEnv() // FromEnv returns the run id when ERBRUS_RUN_ID is set
	return id
}
```

Check what the second return value of `client.FromEnv` is (the msg code uses it as the channel id). If it is the channel, read `ERBRUS_RUN_ID` directly with `strconv.ParseInt(os.Getenv("ERBRUS_RUN_ID"), 10, 64)` instead.

`cli.go`: add cases `"provider"` → `runProvider`, `"screen"` → `runScreen`, `"agents"` → `runAgents` (Task 7), and usage lines:
```
  agents     detect installed agent CLIs; setup installs the erbrus skill (agents list | agents setup)
  provider   show/set a provider in config.yaml through the server (provider show | provider set)
  screen     read or classify a run's terminal (screen capture | screen test)
```

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/cli/ ./internal/client/ 2>&1 | tail -4` → PASS.

```bash
/usr/bin/git add internal/client/client.go internal/cli/provider.go internal/cli/screen.go internal/cli/cli.go internal/cli/provider_test.go internal/cli/msg_test.go
/usr/bin/git commit -m "feat(cli): provider set/show and screen capture/test — the commands the erbrus skill teaches agents"
```

---

### Task 7: `erbrus agents list|setup`

**Files:**
- Create: `internal/cli/agents.go`
- Test: `internal/cli/agents_test.go`

**Interfaces:**
- Consumes: `agents.Detect/Install/Builtins`, `agentskill`, `config.LoadGlobal`, `config.SetProvider`, `client.PutProvider`, `configPath()`.
- Produces: `runAgents(args, stdin io.Reader, stdout, stderr) int`; package vars `agentsHomeDir func() (string, error)`, `agentsLookPath func(string) (string, error)`; pure `parseSelection(input string, n int, checked []bool) ([]int, error)`.

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run to verify failure**.

- [ ] **Step 3: Implement** `internal/cli/agents.go`

```go
package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"erbrus/internal/agents"
	"erbrus/internal/agentskill"
	"erbrus/internal/client"
	"erbrus/internal/config"
)

// Injectable for tests: never probe the developer's real home there.
var (
	agentsHomeDir  = os.UserHomeDir
	agentsLookPath = exec.LookPath
)

const agentsUsage = `usage: erbrus agents list
       erbrus agents setup [--all | --update | --agents id,id]
`

func runAgents(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, agentsUsage)
		return 2
	}
	cfgPath := configPath()
	cfg, cfgErr := config.LoadGlobal(cfgPath)
	home, _ := agentsHomeDir()
	dets := agents.Detect(home, agentsLookPath, cfg)
	switch args[0] {
	case "list":
		if len(dets) == 0 {
			fmt.Fprintln(stdout, "no supported agents detected")
			return 0
		}
		printAgents(stdout, dets)
		return 0
	case "setup":
	default:
		fmt.Fprint(stderr, agentsUsage)
		return 2
	}
	fs := flag.NewFlagSet("agents setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "set up every detected agent")
	update := fs.Bool("update", false, "refresh agents that already have the skill")
	ids := fs.String("agents", "", "comma-separated agent ids to set up")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if len(dets) == 0 {
		fmt.Fprintln(stdout, "no supported agents detected")
		return 0
	}
	var chosen []agents.Detection
	switch {
	case *all:
		chosen = dets
	case *update:
		for _, d := range dets {
			if d.Status.Checked() {
				chosen = append(chosen, d)
			}
		}
	case *ids != "":
		for _, id := range strings.Split(*ids, ",") {
			id = strings.TrimSpace(id)
			found := false
			for _, d := range dets {
				if d.Agent.ID == id {
					chosen = append(chosen, d)
					found = true
				}
			}
			if !found {
				fmt.Fprintf(stderr, "agents setup: unknown or undetected agent %q\n", id)
				return 2
			}
		}
	default:
		printAgents(stdout, dets)
		fmt.Fprint(stderr, "Apply? [enter]=checked / a=all / numbers (e.g. 1,3) / [q]uit: ")
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(stderr, "agents setup: no selection (non-interactive?); use --all, --update, or --agents")
			return 2
		}
		checked := make([]bool, len(dets))
		for i, d := range dets {
			checked[i] = d.Status.Checked()
		}
		idx, err := parseSelection(strings.TrimSpace(line), len(dets), checked)
		if err != nil {
			fmt.Fprintln(stderr, "agents setup:", err)
			return 2
		}
		for _, i := range idx {
			chosen = append(chosen, dets[i])
		}
	}
	if len(chosen) == 0 {
		fmt.Fprintln(stdout, "nothing selected")
		return 0
	}

	failed := false
	c, _, _ := client.FromEnv()
	serverUp := c != nil && pingServer(c)
	for _, d := range chosen {
		if err := agents.Install(d); err != nil {
			fmt.Fprintf(stderr, "agents setup: %s: %v\n", d.Agent.Label, err)
			failed = true
			continue
		}
		verb := "installed"
		if d.Status != agents.StatusNew {
			verb = "refreshed"
		}
		fmt.Fprintf(stdout, "✓ %s %s skill v%d → %s\n", verb, d.Agent.Label, agentskill.Version, d.SkillPath)
		if d.Configured {
			continue
		}
		if cfgErr != nil {
			fmt.Fprintf(stderr, "agents setup: cannot add provider %s: %v\n", d.Agent.ID, cfgErr)
			failed = true
			continue
		}
		if err := addProvider(c, serverUp, cfgPath, d.Agent); err != nil {
			fmt.Fprintf(stderr, "agents setup: provider %s: %v\n", d.Agent.ID, err)
			failed = true
			continue
		}
		note := ""
		if d.Agent.Note != "" {
			note = " (" + d.Agent.Note + ")"
		}
		if serverUp {
			fmt.Fprintf(stdout, "✓ provider %s added%s and applied to the running erbrus\n", d.Agent.ID, note)
		} else {
			fmt.Fprintf(stdout, "✓ provider %s added to %s%s — restart erbrus serve to pick it up\n", d.Agent.ID, cfgPath, note)
		}
	}
	fmt.Fprintln(stdout, "\nNext: let an agent refine its own rules — spawn it with the prompt")
	fmt.Fprintln(stdout, `  "configure yourself as an erbrus provider using the erbrus skill"`)
	fmt.Fprintln(stdout, "or check the screen rules under Settings → Screen rules.")
	if failed {
		return 1
	}
	return 0
}

func pingServer(c *client.Client) bool {
	_, err := c.ListProjects()
	return err == nil
}

// addProvider prefers the running server (writes the file AND applies
// live); without one it edits config.yaml directly.
func addProvider(c *client.Client, serverUp bool, cfgPath string, a agents.Agent) error {
	p := a.Provider
	if serverUp {
		_, err := c.PutProvider(a.ID, map[string]any{
			"command": p.Command, "default_model": p.DefaultModel, "prompt": p.Prompt,
			"screen_working": nz(p.ScreenWorking), "screen_waiting": nz(p.ScreenWaiting), "screen_question": nz(p.ScreenQuestion),
		})
		return err
	}
	doc, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := config.SetProvider(doc, a.ID, config.ProviderPatch{
		Command: config.Str(p.Command), DefaultModel: config.Str(p.DefaultModel), Prompt: config.Str(p.Prompt),
		Working: config.List(p.ScreenWorking), Waiting: config.List(p.ScreenWaiting), Question: config.List(p.ScreenQuestion),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepathDir(cfgPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfgPath, out, 0o644)
}

func nz(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func printAgents(w io.Writer, dets []agents.Detection) {
	fmt.Fprintln(w, "Detected agents:")
	for i, d := range dets {
		box := "[ ]"
		if d.Status.Checked() {
			box = "[x]"
		}
		prov := "provider: configured"
		if !d.Configured {
			prov = "provider: will be added"
			if d.Agent.Note != "" {
				prov += " (" + d.Agent.Note + ")"
			}
		}
		fmt.Fprintf(w, "  %d. %s %-12s %-45s %-11s %s\n", i+1, box, d.Agent.Label, d.SkillPath, d.Status, prov)
	}
}

// parseSelection: "" → checked ones, "a" → all, "q" → none, "1,3" → those.
func parseSelection(in string, n int, checked []bool) ([]int, error) {
	switch strings.ToLower(in) {
	case "":
		var out []int
		for i := 0; i < n; i++ {
			if checked[i] {
				out = append(out, i)
			}
		}
		return out, nil
	case "a":
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out, nil
	case "q":
		return nil, nil
	}
	var out []int
	for _, tok := range strings.Split(in, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil || v < 1 || v > n {
			return nil, fmt.Errorf("invalid selection %q", tok)
		}
		out = append(out, v-1)
	}
	return out, nil
}
```

`filepathDir` is `filepath.Dir` (import `path/filepath`; written out here to keep the import list obvious). Wire `case "agents": return runAgents(args[1:], os.Stdin, stdout, stderr)` in `cli.go`.

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/cli/ 2>&1 | tail -3` → PASS. Also run `go vet ./...`.

```bash
/usr/bin/git add internal/cli/agents.go internal/cli/agents_test.go internal/cli/cli.go
/usr/bin/git commit -m "feat(cli): erbrus agents list|setup — detect agent CLIs, install the erbrus skill, seed providers"
```

---

### Task 8: Prompt delivery by paste

**Files:**
- Modify: `internal/server/runs.go` (spawnCore step 7 and after spawn), create `internal/server/paste.go`
- Modify: `internal/cli/start.go` (fg note)
- Test: `internal/server/paste_test.go`

**Interfaces:**
- Produces: `func pasteMode(p config.Provider) bool`, `func (s *Server) deliverPrompt(run store.AgentRun, prompt string)`; package vars `pastePoll = 500ms`, `pasteDeadline = 60s`, `pasteMax = 30m`, `pasteSettle = 2s`.

- [ ] **Step 1: Write the failing tests**

```go
package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
)

func fastPaste(t *testing.T) {
	t.Helper()
	oldPoll, oldDead, oldSettle := pastePoll, pasteDeadline, pasteSettle
	pastePoll, pasteDeadline, pasteSettle = 5*time.Millisecond, 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { pastePoll, pasteDeadline, pasteSettle = oldPoll, oldDead, oldSettle })
}

func TestPasteModeDetection(t *testing.T) {
	if pasteMode(config.Provider{Command: `k "{prompt}"`}) || !pasteMode(config.Provider{Command: `k {args}`}) ||
		!pasteMode(config.Provider{Command: `k "{prompt}"`, Prompt: "paste"}) || pasteMode(config.Provider{Command: `k`, Prompt: "arg"}) {
		t.Fatal("pasteMode")
	}
}

func spawnPasteProvider(t *testing.T, rules bool) (*fakeSpawner, int64, int64) {
	t.Helper()
	fastPaste(t)
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:5", alive: map[spawn.Handle]bool{"s:5": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	p := config.Provider{Command: `kimi --yolo {args}`}
	if rules {
		p.ScreenWaiting = []string{`^│ >[^\n]*\n╰`}
		p.ScreenQuestion = []string{`Trust this folder\?`}
	}
	testSrv.cfg.Providers["kimi"] = p
	testSrv.setRules("kimi", nil)
	if rules {
		r, _ := screenCompile(p)
		testSrv.setRules("kimi", &r)
	}
	ch1, _ := twoChannels(t, ts.URL, root)
	resp, out := doJSON(t, "POST", ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "kimi", "name": "kimi", "prompt": "do the thing"})
	if resp.StatusCode != 200 {
		t.Fatalf("spawn: %d %v", resp.StatusCode, out)
	}
	return fs, ch1, int64(out["id"].(float64))
}

func TestPasteDeliversOnWaiting(t *testing.T) {
	fs, ch1, _ := spawnPasteProvider(t, true)
	// The command line carries no prompt.
	if strings.Contains(fs.specs[0].Command[len(fs.specs[0].Command)-1], "do the thing") {
		t.Fatal("prompt must not be on the command line in paste mode")
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "Trust this folder?\n❯ Trust\n", Activity: time.Now()})
	time.Sleep(60 * time.Millisecond)
	if len(fs.sent) != 0 {
		t.Fatal("must not paste over a dialog")
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "╭──╮\n│ >   │\n╰──╯\n", Activity: time.Now()})
	deadline := time.Now().Add(2 * time.Second)
	for len(fs.sent) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.sent) != 1 || !strings.Contains(fs.sent[0], "do the thing") || !strings.Contains(fs.sent[0], "erbrus msg send") {
		t.Fatalf("sent = %v", fs.sent)
	}
	time.Sleep(50 * time.Millisecond)
	if len(fs.sent) != 1 {
		t.Fatal("pasted twice")
	}
	if got := systemBodies(t, testSrv.st, ch1); !strings.Contains(strings.Join(got, "\n"), "prompt delivered") {
		t.Fatalf("no delivered message: %v", got)
	}
}

func TestPasteWithoutRulesWaitsForStableScreen(t *testing.T) {
	fs, _, _ := spawnPasteProvider(t, false)
	fs.setScreen("s:5", spawn.Screen{Raw: "booting…\n", Activity: time.Now()})
	deadline := time.Now().Add(2 * time.Second)
	for len(fs.sent) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fs.sent) != 1 {
		t.Fatalf("stable screen must trigger paste: %v", fs.sent)
	}
}

func TestPasteTimeoutPostsNotDelivered(t *testing.T) {
	fs, ch1, _ := spawnPasteProvider(t, true)
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (1s · x)\n", Activity: time.Now()}) // never waiting
	time.Sleep(pasteDeadline + 200*time.Millisecond)
	if len(fs.sent) != 0 {
		t.Fatal("must not paste while working")
	}
	if got := systemBodies(t, testSrv.st, ch1); !strings.Contains(strings.Join(got, "\n"), "NOT delivered") {
		t.Fatalf("no timeout message: %v", got)
	}
}

func TestArgModeNeverPastes(t *testing.T) {
	fastPaste(t)
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:5", alive: map[spawn.Handle]bool{"s:5": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	doJSON(t, "POST", ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "claude-code", "name": "c", "prompt": "hi"})
	fs.setScreen("s:5", spawn.Screen{Raw: "──────────\n❯\n", Activity: time.Now()})
	time.Sleep(100 * time.Millisecond)
	if len(fs.sent) != 0 {
		t.Fatalf("arg mode pasted: %v", fs.sent)
	}
	_ = st
}
```

`screenCompile(p)` is a tiny test helper: `screen.Compile(p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion)`. Check how `newTestServer` seeds `cfg.Providers` (it must contain `claude-code`); if the map is nil, initialize it in the helper. The fake spawner records `specs` on Spawn and `sent` on Send already.

- [ ] **Step 2: Run to verify failure**.

- [ ] **Step 3: Implement** `internal/server/paste.go`

```go
package server

import (
	"fmt"
	"strings"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// Paste delivery pacing. Package vars so tests can shorten them.
var (
	pastePoll     = 500 * time.Millisecond
	pasteDeadline = 60 * time.Second // extended while a dialog is up
	pasteMax      = 30 * time.Minute // hard cap, dialog or not
	pasteSettle   = 2 * time.Second  // no rules: paste once the screen sat still this long
)

// pasteMode: the prompt is typed into the terminal instead of passed on
// the command line — explicitly (prompt: paste) or because the template
// has nowhere to put it.
func pasteMode(p config.Provider) bool {
	switch p.Prompt {
	case "paste":
		return true
	case "arg":
		return false
	}
	return !strings.Contains(p.Command, "{prompt}")
}

// deliverPrompt waits for the agent's input box and pastes prompt once.
// Runs in its own goroutine right after a tmux spawn.
func (s *Server) deliverPrompt(run store.AgentRun, prompt string) {
	h := spawn.Handle(run.TmuxTarget)
	rules := s.rulesFor(run.Provider)
	canSeeWaiting := len(rules.Waiting) > 0
	start := time.Now()
	deadline := start.Add(pasteDeadline)
	var lastRaw string
	var stableSince time.Time
	fail := func(reason string) {
		s.system(run.ChannelID, fmt.Sprintf("%s: prompt NOT delivered (%s) — paste it yourself", run.AgentName, reason))
	}
	for {
		time.Sleep(pastePoll)
		if cur, ok, _ := s.st.RunByID(run.ID); !ok || (cur.Status != "running" && cur.Status != "starting") {
			return
		}
		sc, err := s.spawner.Capture(h)
		if err != nil {
			fail("window gone")
			return
		}
		lines := screen.Tail(screen.Strip(sc.Raw), 15)
		st := screen.Classify(rules, lines)
		now := time.Now()
		ready := false
		switch {
		case st == screen.Question:
			deadline = now.Add(pasteDeadline) // the human is answering; keep waiting
		case st == screen.Waiting:
			ready = true
		case !canSeeWaiting && st == screen.Unknown && len(lines) > 0:
			if sc.Raw != lastRaw {
				lastRaw, stableSince = sc.Raw, now
			} else if now.Sub(stableSince) >= pasteSettle {
				ready = true
			}
		}
		if ready {
			if err := s.spawner.Send(h, prompt); err != nil {
				fail(err.Error())
				return
			}
			s.system(run.ChannelID, fmt.Sprintf("%s: prompt delivered", run.AgentName))
			return
		}
		if now.After(deadline) || now.Sub(start) > pasteMax {
			label := string(st)
			if label == "" {
				label = "unknown"
			}
			fail("no input box within " + shortDur(now.Sub(start)) + ", screen state " + label)
			return
		}
	}
}
```

`runs.go` step 7 becomes:
```go
	preamble := integrate.Preamble(s.erbrusBin, agentName, channel.Name)
	fullPrompt := integrate.AssemblePrompt(preamble, promptText, handoffContext)
	paste := pasteMode(s.cfg.Providers[providerName])
	cmdPrompt := fullPrompt
	if paste {
		cmdPrompt = "" // typed in later by deliverPrompt
	}
	command, err := provider.Registry(s.cfg.Providers).Render(providerName, model, fullArgs, cmdPrompt)
```
After `s.system(req.ChannelID, fmt.Sprintf("%s spawned in tmux %s", agentName, handle))` add:
```go
	if paste {
		go s.deliverPrompt(run, fullPrompt)
	}
```
For the fg path, include `Paste bool` and `Prompt string` in `fgJSON` when `paste` (check the struct; add fields `Paste bool `json:"paste,omitempty"`` and `Prompt string `json:"prompt,omitempty"``). In `cli/start.go`, after printing the fg command, when `res.Paste` print to stderr: `this provider takes the prompt by paste; type it into the agent yourself:` followed by the prompt.

The provider lookup `s.cfg.Providers[providerName]` must happen before the request-model validation drops unknown providers; `spawnCore` already errors on unknown providers via `Render`, so reading the map here is safe.

- [ ] **Step 4: Run, commit**

Run: `go test ./internal/server/ -race 2>&1 | tail -3 && go test ./internal/cli/ 2>&1 | tail -2` → PASS.

```bash
/usr/bin/git add internal/server/paste.go internal/server/paste_test.go internal/server/runs.go internal/cli/start.go
/usr/bin/git commit -m "feat(spawn): prompt by paste for providers without {prompt} (kimi) — wait for the input box, then Send

Behavior change: providers whose command lacks {prompt} previously lost the prompt; now it is pasted."
```

---

### Task 9: Verification, docs pointer, binary

- [ ] **Step 1: Full suite and hygiene**

Run: `gofmt -l internal cmd; go vet ./... && go test ./... 2>&1 | tail -16` → all PASS, nothing listed by gofmt.

- [ ] **Step 2: Live check of the skill install and kimi on the throwaway instance**

Use the scratchpad config (`XDG_CONFIG_HOME=<scratch>/xcfg`, port 7499) with `ERBRUS_CONFIG` pointing at that config and `ERBRUS_URL=http://127.0.0.1:7499`; run `bin/erbrus agents list`, then `bin/erbrus agents setup --agents kimi` while the throwaway server runs (expect "applied to the running erbrus"), then `bin/erbrus provider show kimi`. Spawn kimi into the erbrus project's `general` channel from the UI; in the run's screen page answer the trust dialog; confirm the system message "kimi: prompt delivered" and the pasted preamble on screen. Note: this writes `~/.kimi-code/skills/erbrus/SKILL.md` for real, which is the intended effect; remove the kimi workspace-trust entry afterwards as before if the scratch folder was used.

- [ ] **Step 3: Skill in action**

In a Claude Code session with `~/.claude/skills/erbrus/SKILL.md` installed (`bin/erbrus agents setup --agents claude-code`), ask: "configure junie as an erbrus provider using the erbrus skill" and watch it use `erbrus provider set`, `erbrus screen capture`, `erbrus screen test`. Record what it got wrong in the spec's amendments section.

- [ ] **Step 4: Build and hand over**

Run: `./build.sh` → `/mnt/t/others/erbrus/.claude/worktrees/agents-setup/bin/erbrus`.

---

## Self-review

- Spec coverage: skill (1), registry/detect/install (2), config prompt + SetProvider (3), quoted model + raw drop (4), API routes (5), client + CLI provider/screen (6), agents setup with server-first provider add and restart note (7), paste delivery with caps and fg note (8), verification (9). Error table rows map to Tasks 5, 7, 8.
- Type consistency: `config.ProviderPatch{Command, DefaultModel, Prompt *string; Working, Waiting, Question *[]string}` used identically in Tasks 3, 5, 7; `client.PutProvider(name, map[string]any)` in Tasks 6, 7; `agents.Detection{Agent, BinaryPath, SkillPath, Status, Configured}` in Tasks 2, 7; `pasteMode(config.Provider)` in Task 8 only.
- Placeholders: none; open checks are named ("check how newTestServer seeds cfg.Providers", "check FromEnv's second return") with the fallback spelled out.
