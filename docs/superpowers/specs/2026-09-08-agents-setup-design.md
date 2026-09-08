# erbrus — agent discovery, the erbrus skill, and self-configuring providers

Date: 2026-09-08 (late evening). Status: design approved in chat
("continue"); implementation on branch `worktree-agents-setup` in
`/mnt/t/others/erbrus/.claude/worktrees/agents-setup`.

Predecessors: the live-screen, screen-state and web-terminal specs in this
directory; the codex execpolicy rule in `internal/integrate`.

## Purpose

Today a new agent CLI becomes usable in erbrus only after a human writes a
`providers:` entry by hand and guesses its screen rules. The request
(2026-09-08): "provide a command to discover agents and set them up with
the skill so agents can on their own configure themselves as erbrus
agents", and "`erbrus agents setup` should find all agents without the
erbrus skill or with an old one, provide a list for the user to select
which agent to set up".

So: erbrus knows the agent CLIs it supports, installs a versioned **erbrus
skill** into each one's skill directory, seeds a provider entry from
built-in defaults, and gives agents a CLI to inspect their own screens and
save their own provider settings. The user's gigagit `gg init` does the
same for the gg skill; this follows its shape (hardcoded registry,
versioned marker, numbered selection).

## Facts this design rests on (verified on this machine, 2026-09-08)

| agent | binary, version | skill location (user scope) | prompt / model / auto-approve |
|---|---|---|---|
| Claude Code | `claude` 2.1.263 | `~/.claude/skills/<name>/SKILL.md` | positional prompt; `--model`; already configured |
| Codex | `codex` 0.153.4 | `~/.codex/skills/<name>/SKILL.md` (dir created by codex itself) | positional prompt; `--model`; already configured |
| Kimi Code | `kimi` 0.41.0 | `~/.kimi-code/skills/<name>/SKILL.md` (gg's skill lives there) | **no interactive initial prompt** (`-p` is non-interactive); `--model`; `--yolo` |
| Junie | `junie` 26.8.24 | `~/.junie/skills/<name>/SKILL.md` (`--skill-default-locations` per user) | positional prompt (as in the settings-page example) |
| Antigravity | `agy` 1.1.4 | `~/.gemini/config/skills/<name>/SKILL.md` (gg's skill lives there); detect `~/.gemini/antigravity-cli` | `-i "<prompt>"` runs an initial prompt interactively; `--model`; `--dangerously-skip-permissions`; model names contain spaces and parentheses, e.g. `Gemini 3.6 Flash (High)` |

Gemini CLI is sunset (user, 2026-09-08) and is not in the table.

Kimi captures (throwaway tmux window, scratch folder): a "Trust this
folder?" dialog (`↑↓ navigate · Enter select · Esc exit`, `❯ Trust this
folder`); an idle input box drawn as `╭─╮ / │ > … │ / ╰─╯` above a status
line `Ask When Needed  K3 thinking: high  …/cwd`; while busy a moon-phase
spinner (`🌔 Retrying (2/10) · …`). The Kimi API returned 500 during the
probe, so the ordinary "thinking" screen is unverified.

All five CLIs read the same skill form: a directory named after the skill
holding `SKILL.md` with `name` and `description` frontmatter.

erbrus today: `{model}` is spliced unquoted and validated against
`^[A-Za-z0-9._:-]*$`; the prompt reaches the agent only through the
command template (`{prompt}`, shell-quoted); `config.SetProviderScreenRules`
rewrites `config.yaml` with comments kept but requires the provider to
exist; the settings page can test and save screen rules and applies them
live.

## Scope

In:

1. `internal/agentskill`: the embedded, versioned erbrus skill.
2. `internal/agents`: the hardcoded agent registry, detection, install.
3. `erbrus agents setup` (and `erbrus agents list`).
4. Provider config: `prompt: arg | paste`; `{model}` shell-quoted.
5. Prompt delivery by paste for providers whose command carries no prompt.
6. API + CLI for agents: read a run's screen, test rules, save a provider.

Out (see the last section): an automatic tuning run, project-scoped skill
locations, agents outside the table, editing provider commands in the web
settings page.

## 1. The erbrus skill (`internal/agentskill`)

`erbrus.md` is embedded (`go:embed`), `Version` is a Go constant bumped
with every content change. Rendered form:

```
---
name: erbrus
description: Use when running as an erbrus agent (report to your channel with erbrus msg send, reply to forwards) or when configuring an AI coding CLI as an erbrus provider (command template, prompt delivery, screen rules).
---

<!-- erbrus:erbrus:v1 -->

<body>
```

`HasMarker(content)` / `InstalledVersion(content)` parse
`erbrus:erbrus:v(\d+)`; installed files are erbrus-owned and overwritten
whole.

Body outline (the file is the deliverable; this is its table of contents):

1. **Working as an erbrus agent.** What the preamble already says, in
   full: `msg send --report --md '…'`, the stdin heredoc form for text
   with quotes/backticks, `--file`, replying to a forwarded message with
   `--channel <origin>`, the environment (`ERBRUS_URL`, `ERBRUS_TOKEN`,
   `ERBRUS_CHANNEL`, `ERBRUS_RUN_ID`), "do nothing until asked".
2. **Configuring a CLI as an erbrus provider.** The `providers.<name>`
   keys: `command` with `{model}`, `{args}`, `{prompt}`; `default_model`;
   `prompt: arg|paste` and when paste is needed; the three screen rule
   lists and exactly how classification works (stripped, trimmed tail of
   15 lines, joined with newlines, `(?m)` RE2, working checked first, then
   waiting, then question, unknown otherwise; a non-empty list replaces
   all built-ins for that provider).
3. **The workflow**, step by step: find the CLI's flags with `--help`;
   pick the template; save it with `erbrus provider set`; spawn the CLI
   through erbrus (or note your own run id from `ERBRUS_RUN_ID`); read
   screens with `erbrus screen capture --run <id>` at the idle prompt,
   while busy, and on a dialog; write rules; check them with
   `erbrus screen test --provider <name> --run <id> --working … --waiting …
   --question …`; save; report what was configured.
4. **Reference**: the built-in rules for claude-code and codex as worked
   examples, and the kimi/antigravity defaults from the registry.

## 2. Registry (`internal/agents`)

```go
type Agent struct {
    ID       string          // provider name: claude-code, codex, kimi, junie, antigravity
    Label    string
    Binary   string          // looked up on PATH
    Home     string          // "~/.claude" …: presence means "installed"
    Skill    string          // "~/.claude/skills/erbrus/SKILL.md"
    Provider config.Provider // defaults seeded into config.yaml when absent
    Note     string          // one line shown in the list (e.g. "prompt by paste")
}
func Builtins() []Agent
```

Entries and their provider defaults:

| ID | Home | Skill | Provider default |
|---|---|---|---|
| claude-code | ~/.claude | ~/.claude/skills/erbrus/SKILL.md | `claude --model {model} {args} "{prompt}"` (matches the settings example; screen rules built-in) |
| codex | ~/.codex | ~/.codex/skills/erbrus/SKILL.md | `codex --model {model} {args} "{prompt}"` |
| kimi | ~/.kimi-code | ~/.kimi-code/skills/erbrus/SKILL.md | `kimi --yolo --model {model} {args}`, `default_model: kimi-code/k3`, `prompt: paste`, `screen_working: ['^[🌑🌒🌓🌔🌕🌖🌗🌘] ', 'Retrying \(\d+/\d+\)']`, `screen_waiting: ['^│ >[^\n]*\n╰']`, `screen_question: ['Trust this folder\?', 'Enter select', 'Esc exit']` |
| junie | ~/.junie | ~/.junie/skills/erbrus/SKILL.md | `junie {args} "{prompt}"` |
| antigravity | ~/.gemini/antigravity-cli | ~/.gemini/config/skills/erbrus/SKILL.md | `agy --dangerously-skip-permissions --model "{model}" {args} -i "{prompt}"`, `default_model: Gemini 3.6 Flash (High)` |

```go
type Status int // StatusNew, StatusOutdated, StatusUpToDate
type Detection struct {
    Agent      Agent
    BinaryPath string // "" when not on PATH
    SkillPath  string // absolute
    Status     Status
    Configured bool   // providers.<ID> exists in the loaded global config
}
// Detect lists registry entries whose Home exists or whose Binary is on
// PATH. homeDir "" skips home probing (tests); lookPath is injectable.
func Detect(homeDir string, lookPath func(string) (string, error), cfg config.Global) []Detection
// Install writes the rendered skill to d.SkillPath (mkdir -p, 0644).
func Install(d Detection) error
```

## 3. `erbrus agents setup` / `erbrus agents list`

`list` prints the detection table and exits. `setup`:

```
Detected agents:
  1. [x] Claude Code   ~/.claude/skills/erbrus/SKILL.md   outdated (v1 → v2)   provider: configured
  2. [ ] Kimi Code     ~/.kimi-code/skills/erbrus/SKILL.md new                 provider: will be added (prompt by paste)
  3. [x] Antigravity   ~/.gemini/config/skills/erbrus/SKILL.md up to date      provider: configured
Apply? [enter]=checked / a=all / numbers (e.g. 1,3) / [q]uit:
```

Checked by default: targets that already carry the skill (any version),
exactly gg's rule. Flags: `--all`, `--update` (checked only), `--agents
kimi,junie` (by ID), any of which skips the prompt; with no TTY and no
flag the command explains and exits 2. For each chosen agent: write the
skill file; if `providers.<ID>` is absent in `config.yaml`, add the
registry defaults via `config.EnsureProvider` (yaml.v3 nodes, comments
kept; the `providers:` mapping is created when missing); never touch an
existing provider. Output per agent: `✓ installed/refreshed <label> →
<path>` and `✓ provider <ID> added to <config path>` when applicable. The
command ends with:

```
Next: let an agent refine its own rules — spawn it with the prompt
  "configure yourself as an erbrus provider using the erbrus skill"
or check the screen rules under Settings → Screen rules.
```

The config path is `configPath()` (honors `ERBRUS_CONFIG`); the home dir
is `os.UserHomeDir()`, overridable by the package variable
`agentsHomeDir` for tests.

## 4. Provider config: `prompt` and quoted `{model}`

`config.Provider` gains `Prompt string `yaml:"prompt"`` with values `arg`
(default) and `paste`. `provider.Render` shell-quotes the model exactly as
it quotes the prompt, replacing the template's own quotes around `{model}`
when present. `validModel` then only rejects shell metacharacters
(`;|&$()\`<>\n` and quotes are fine inside single quotes, so the check
becomes: no newline, no NUL); the existing tests that reject `$(` are
adjusted to the new rule. Presets keep working unchanged.

## 5. Prompt by paste

In `spawnCore`, after rendering: `pasteMode := p.Prompt == "paste" ||
(p.Prompt == "" && !strings.Contains(p.Command, "{prompt}"))`. In paste
mode the command is rendered with an empty prompt and, after a successful
tmux spawn, `go s.deliverPrompt(run, fullPrompt, rules)`:

- Poll `Capture` every 500 ms for up to `pasteDeadline` (60 s, package
  var), plus as long as the screen is in the **question** state (a trust
  dialog waits for the human; the watcher's normal "needs your input"
  message covers it).
- Deliver when `Classify` says **waiting**; when the provider has no
  rules that can say waiting (generic rules only match a bare prompt
  glyph), deliver when the screen has been non-empty and unchanged for
  2 s.
- Delivery is `Spawner.Send(handle, fullPrompt)` (bracketed paste, Enter,
  verify/retry as today). Post `"<agent>: prompt delivered"` or
  `"<agent>: prompt NOT delivered (<reason>) — paste it yourself"` as a
  system message; publish a `run` event.
- The fg path cannot paste: `start --fg` with a paste provider prints the
  prompt to stdout after the command line and says so.

The run row keeps `Prompt` as today, so the screen page and the rail show
what was meant to be sent.

## 6. API and CLI for agents

Routes (UI-level, no bearer required, same origin guard as the rest):

- `GET /api/runs/{id}/screen` → `{"state":"waiting","lines":[…15 stripped tail lines…],"options":[{key,label}],"cols":134,"rows":36,"dead":false}`; 404 without a tmux window.
- `POST /api/providers/{name}/screen-test` body `{"run":5,"working":[…],"waiting":[…],"question":[…]}` → `{"state":"…","lines":[{"text":"…","match":"working|waiting|question|"}]}`; 422 on a bad pattern. Unknown provider names are allowed here (an agent may test before saving).
- `PUT /api/providers/{name}` body `{"command":"…","default_model":"…","prompt":"arg|paste","screen_working":[…],"screen_waiting":[…],"screen_question":[…]}`; omitted fields keep their value, present-but-empty lists clear. Creates the provider when missing. Rewrites `config.yaml` through `config.SetProvider` (a generalization of `SetProviderScreenRules`: same node editing, also sets `command`, `default_model`, `prompt`), then updates `s.cfg.Providers` and `setRules` live. 400 when the server has no config path.

CLI (`internal/cli/provider.go`, `screen.go`), all through `client`:

```
erbrus provider set <name> [--command S] [--default-model S] [--prompt arg|paste]
                          [--working RE]… [--waiting RE]… [--question RE]… [--clear-rules]
erbrus provider show <name>
erbrus screen capture --run N            # prints state, then the lines
erbrus screen test --provider P --run N [--working RE]… [--waiting RE]… [--question RE]…
```

Repeatable flags collect lists; `--clear-rules` sends the three lists
empty. `provider show` prints the effective provider from
`GET /api/providers/{name}` (added for symmetry, returns the config entry
and whether rules are built-in).

## Error handling

| Situation | Behavior |
|---|---|
| No agents detected | `agents setup` prints "no supported agents detected" and exits 0 |
| Skill dir not writable | error line for that agent, continue with the rest, exit 1 at the end |
| `config.yaml` unparsable | `agents setup` installs skills, reports the config error, exit 1 |
| Provider exists | skill refreshed, provider untouched, say so |
| Paste never possible (window died) | "prompt NOT delivered (window gone)" system message |
| Paste timeout while working (agent started without input box) | after `pasteDeadline`: NOT delivered message with the last state |
| `PUT /api/providers/{name}` with bad pattern | 422, nothing written |
| `screen capture` for a finished run | 404 "run has no tmux window" |

## Security

Skill files are written only under the registry's paths in the user's
home; `agents setup` never writes into repositories. The provider API is
as open as the settings page (localhost UI, origin-guarded). The model
value is shell-quoted; args keep the metacharacter check.

## Testing

- `agentskill`: rendered file has frontmatter, marker, body; `HasMarker`,
  `InstalledVersion` on all forms; version constant matches the marker.
- `agents`: `Detect` with a temp home and a fake `lookPath`: installed /
  not installed / new / outdated / up to date / configured; `Install`
  creates dirs and overwrites; entries' provider templates render with
  `provider.Render` without error (guards typos in the table).
- `cli agents`: `--agents kimi` writes the skill and adds the provider to
  a temp config; `--update` touches only existing files; interactive
  selection parsing (`1,3`, `a`, enter, `q`) as pure functions.
- `config.SetProvider`: creates `providers:` and the entry, keeps comments,
  updates fields, clears lists, round-trips through `LoadGlobal`.
- `provider.Render`: model with spaces/parentheses is quoted; template
  quotes around `{model}` replaced; unchanged output for plain models.
- `server`: paste mode with `fakeSpawner`: screen goes unknown → waiting →
  `Send` called once with the assembled prompt, system message posted;
  question first delays delivery; timeout posts NOT delivered; arg mode
  never calls `Send`. New API routes: screen JSON, screen-test JSON and
  422, provider PUT creates/updates config and applies live.
- Manual (throwaway instance on 7499): `erbrus agents setup --agents kimi`;
  spawn kimi; watch the trust dialog → answer → prompt pasted → agent
  reports; then in a Claude Code session with the skill installed, ask it
  to configure junie and watch it use the CLI.

## Out of scope

- **Automatic tuning run** (erbrus spawning the agent into a scratch
  channel with "configure yourself" and collecting screen samples for it):
  after the skill has been used by hand.
- **Project-scoped skills** (`.claude/skills` in a repo): global is enough
  for erbrus-spawned agents.
- **Agents outside the table**: `erbrus provider set` covers them by hand.
- **Provider command editing in the web settings page**: the CLI/API is
  the editing path for now; the page keeps its screen-rules editor.
- **Windows**: `agents setup` works (paths under the Windows home) but the
  prompt-paste and screen features need tmux as before.
