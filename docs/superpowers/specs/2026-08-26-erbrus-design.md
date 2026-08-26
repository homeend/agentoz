# erbrus — v1 design

Date: 2026-08-26
Status: approved design, pre-implementation
Wireframes: https://claude.ai/code/artifact/952fd05f-1ad8-4da2-98d7-a763a657db11

## Purpose

erbrus is a local, single-user Go server that arranges communication between
coding agents (and the human) into chat channels bound to projects, git
repositories, and worktrees. Agents from different providers (Claude Code,
Codex CLI, JetBrains Junie, Kimi K3) are spawned into tmux, do their work,
and post results ("reports") back to channels. A report — including its
artifacts and provenance — can be handed to the next agent, in the same
project or in a different one, so multi-repo features can flow between
workspaces with full context.

## v1 scope

- Go server + web UI (server-rendered, htmx, SSE).
- Projects bound to git repos; channels bound to repos and worktrees.
- Worktree detection via the `wt` tool (`wt list --json`).
- Agent presets: partially preconfigured spawnable agents.
- Tmux spawner (managed sessions and attach-to-existing).
- Agent reporting via injected instructions, stop hooks, and an exit wrapper.
- Report handoff: forward to any channel, or spawn an agent from a report,
  across projects.
- CLI: `erbrus serve | start | init | msg send | msg read`.
- Layered settings: global (`~/.config/erbrus/config.yaml`) + per-repo
  (`~/.erbrus/repos/<encoded-repo-path>/.erbrus.yaml`).

### Non-goals (parked for later milestones)

- Tickets / task tracking.
- Structured knowledge store beyond channel history and artifacts.
- Zellij and fully headless spawner implementations (interface allows them).
- MCP adapter for the agent API.

## Architecture

One Go binary, `erbrus`, acting as server (`erbrus serve`) and as client
(all other subcommands). Layout:

```
cmd/erbrus/          main, subcommand wiring (cobra or stdlib flag)
internal/config/     layered YAML config: defaults <- global <- per-repo
internal/store/      SQLite (modernc.org/sqlite), migrations
internal/provider/   provider registry, command templates
internal/preset/     agent presets (merge of global + project presets)
internal/spawn/      AgentSpawner interface, TmuxSpawner, exec wrapper
internal/integrate/  per-provider integration: prompt preamble, hooks
internal/server/     HTTP API + SSE + web UI handlers
internal/web/        html/template templates, htmx partials, static assets
internal/client/     client-side API calls used by CLI subcommands
internal/wt/         worktree detection (wt list --json, git fallback)
```

Dependencies kept minimal: chi (router), modernc.org/sqlite, gopkg.in/yaml.v3.
htmx is a vendored static asset. No Node toolchain.

### Data locations

- Config: `~/.config/erbrus/config.yaml` (global),
  `~/.erbrus/repos/<encoded-repo-path>/.erbrus.yaml` (per project — kept
  OUTSIDE the repo so working trees stay clean). The encoding replaces every
  `/` in the repo's absolute path with `-`, keeping the leading dash — the
  same scheme Claude uses for project dirs (e.g. `/home/homeend/git-focus`
  → `-home-homeend-git-focus`).
- State: `~/.local/share/erbrus/erbrus.db` (SQLite).
- Artifacts: `~/.local/share/erbrus/artifacts/<message-id>/<filename>`.
- Logs: `~/.local/share/erbrus/logs/`.

XDG env vars are honored when set.

## Data model

```
projects    id, name (unique), repo_path (unique, absolute), created_at
channels    id, project_id, name, worktree_path (nullable, absolute),
            branch (nullable), archived, created_at
messages    id, channel_id, kind (message|report|system),
            author_kind (agent|human|system), agent_run_id (nullable),
            body, origin_message_id (nullable, for forwards/handoffs),
            created_at
artifacts   id, message_id, filename, path, size, created_at
agent_runs  id, channel_id, preset_name (nullable), provider, agent_name,
            model (nullable), extra_args (nullable), prompt,
            workdir (absolute), status (starting|running|done|failed|stopped),
            exit_code (nullable), spawner (tmux|fg), tmux_target (nullable),
            token, origin_message_id (nullable), created_at, finished_at
```

Settings never live in the DB — YAML only. The DB holds runtime state and
history.

## Configuration

### Global — `~/.config/erbrus/config.yaml`

```yaml
port: 7420
data_dir: ~/.local/share/erbrus      # optional override
terminal: tmux                        # default spawner
session_pattern: "erbrus-{project}"
wt_bin: wt                            # path to wt binary; auto-detected on PATH

providers:
  claude-code:
    command: 'claude --model {model} {args} "{prompt}"'
    default_model: fable-5
  codex:
    command: 'codex {args} "{prompt}"'
  junie:
    command: 'junie {args} "{prompt}"'
  kimi:
    command: 'kimi --model {model} {args} "{prompt}"'

presets:
  claude:
    provider: claude-code
    model: fable-5
  claude-f:
    provider: claude-code
  codex:
    provider: codex
    prompt: "just some text to have string here"
  kimi:
    provider: kimi
    model: k3
    args: "--max-turns 30"
```

Template placeholders: `{model}`, `{args}`, `{prompt}`. An unset `{model}`
with no `default_model` renders to nothing (flag and value are dropped —
templates use a small renderer, not naive string replace).

### Per-repo — `~/.erbrus/repos/<encoded-repo-path>/.erbrus.yaml`

```yaml
session: erbrus-webshop        # overrides session_pattern
attach_session: ""             # non-empty = attach mode into this session
channel_per_worktree: true
presets:                       # merged over global; same name overrides
  claude:
    provider: codex
    prompt: "Implement using this repo's conventions (see CLAUDE.md)."
```

Merge order everywhere: built-in defaults ← global ← per-repo.

## Provider registry and agent presets

- A **provider** is a config entry: command template plus optional defaults.
  Adding a provider is configuration, not code.
- A **preset** is a named, partially-preconfigured agent: `provider`
  (required) and optional `model`, `prompt`, `args`, `name`. Presets exist
  globally and per project; a project preset with the same name overrides the
  global one.
- Spawning always goes through the same "run request" structure whether it
  comes from the web dialog, a report handoff, or the CLI. A preset only
  prefills the request; every field is editable/overridable at spawn time.

## AgentSpawner

```go
type Spawner interface {
    Spawn(ctx context.Context, run *RunSpec) (Handle, error)
    Stop(ctx context.Context, h Handle) error
    Status(ctx context.Context, h Handle) (RunStatus, error)
}
```

v1 implementations:

- **TmuxSpawner** — two modes, per project config or per-spawn choice:
  - *managed*: creates/uses session named by `session_pattern`
    (e.g. `erbrus-webshop`), one window per run. erbrus only ever kills
    windows/sessions it created.
  - *attach*: opens a new window inside the user-configured existing session
    (`attach_session`). Never restructures or kills anything pre-existing.
- **FgRunner** — used by `erbrus start --fg`: the CLI execs the provider
  command in the current terminal; the run is registered with the server
  first and reports like any other.

All tmux interaction goes through an exec abstraction so tests can fake it.
Zellij/headless later = new Spawner implementations.

### Spawn mechanics

1. Server builds the `RunSpec`: provider command rendered from template,
   env (`ERBRUS_URL`, `ERBRUS_TOKEN`, `ERBRUS_CHANNEL`, `ERBRUS_RUN_ID`),
   workdir (repo root or worktree), prompt (preamble + user/preset prompt +
   appended context).
2. The command is wrapped: `erbrus-wrap` (an internal mode of the same
   binary) execs the provider CLI, and on exit POSTs the exit status to the
   server. This guarantees completion reporting even for providers with no
   hook support.
3. A system message ("<name> spawned in tmux erbrus-webshop:3") lands in the
   channel; status changes stream to the UI via SSE.

## Agent integration (how agents know about erbrus)

Two layers, both wired automatically at spawn:

1. **Instructions (all providers)** — a preamble prepended to the prompt:
   you are agent <name> in channel <channel>; report progress/results with
   `erbrus msg send [--report] [--file <path>]`; read the channel with
   `erbrus msg read`; a report should summarize what was done and attach any
   artifact document produced. Env vars carry server URL, run token, channel.
2. **Hooks (where supported)** —
   - *Claude Code*: erbrus generates a per-run settings JSON with a Stop
     hook running `erbrus msg send --system "prompt finished"` and passes it
     via `--settings <file>`. Instructions can additionally be injected via
     `--append-system-prompt` instead of polluting the task prompt.
   - *Codex*: `notify` config pointed at `erbrus msg send --system`.
   - *Junie, Kimi*: instructions + exit wrapper only in v1.

Per-run tokens make every message attributable to its run; the human posts
through the web UI session (no token).

## Reports and handoff

- A **report** is a message with `kind=report`, optionally carrying artifact
  files and always carrying provenance (project, channel, agent, run).
- Actions on any report, in the UI:
  - **Forward to channel…** — creates a linked copy (`origin_message_id`)
    in any channel of any project; the copy renders with a backlink
    ("report from webshop / #feature-checkout by impl-codex").
  - **Spawn agent from this report** — opens the spawn dialog with the
    report attached as context. The dialog has *target project* and *target
    channel* selectors (default: the report's own). Choosing another project
    makes it a cross-project handoff: the new agent starts in that project's
    repo/worktree, and the report body + artifacts + provenance line are
    appended to its prompt. The source channel gets a system note
    ("handed off to api-gateway / #general").

This replaces pipelines in v1: chains like analyze → implement → review →
fix are performed by repeatedly spawning from reports, entirely under the
user's control.

## CLI

```
erbrus serve                     # run the server (foreground)
erbrus start <preset> [flags]    # spawn a preset agent from the shell
    --model, --prompt, --args, --name, --channel   # overrides
    --fg                         # run in current terminal instead of tmux
    --no-create                  # fail instead of auto-configuring
erbrus init                      # auto-configure only + scaffold the per-repo config in ~/.erbrus/repos/
erbrus msg send [--report] [--file <path>] [--system] <text|stdin>
erbrus msg read [--since <id>] [--limit N]
```

### Auto-configure (default for `start` and `init`)

Run from anywhere inside a repo or worktree:

1. Resolve git root. No project for it? Create one named after the repo
   directory; register worktrees via `wt list --json -r <root>`
   (fallback: `git worktree list --porcelain` when `wt` is absent).
2. Resolve the current worktree. No channel for it? Create it — named from
   the branch (`# wt/checkout-v2`); the main worktree maps to `# general`.
3. Spawn the preset there using merged configs (global + the repo's config
   from `~/.erbrus/repos/<encoded-repo-path>/.erbrus.yaml`).

Everything auto-created is announced in the CLI output and appears in the
web UI immediately. `start` requires a running server and says so plainly
when it is absent (no auto-daemonize in v1).

## Web UI

Server-rendered Go templates + htmx; SSE (`/events`) pushes new messages and
run-status changes into open channel views. Screens (see wireframes):

1. **Channel view** — sidebar (projects → channels), message stream
   (messages, reports with artifact chips and action buttons, system notes),
   composer, right panel (agents in channel with attach/stop + spawnable
   presets).
2. **Projects** — cards with repo path, worktree/channel/agent counts;
   add-project form (repo path → worktree detection).
3. **Spawn dialog** — preset, context (when spawned from a report), target
   project/channel, provider/name/model/args, working directory, terminal
   (managed/attach), prompt.
4. **Settings** — global and per-project side by side; provider table,
   preset tables, session settings. Editing writes the YAML files.

"Attach" on a running agent shows the `tmux attach -t <target>` command to
copy (the server cannot attach your terminal for you).

## HTTP API (agent- and CLI-facing, localhost only)

```
GET  /api/channels/{id}/messages?since=&limit=
POST /api/channels/{id}/messages          {kind, body, files?}   (multipart for files)
POST /api/runs                            spawn request
POST /api/runs/{id}/exit                  {code}                 (from wrapper)
POST /api/runs/{id}/stop
GET  /api/projects, POST /api/projects    (add triggers worktree detection)
POST /api/messages/{id}/forward           {channel_id}
GET  /events                              SSE
```

Agent/CLI auth: `Authorization: Bearer <run token>` (agents) or none for
the human CLI/UI on localhost. Tokens are checked per run and expire when
the run finishes.

## Error handling

- Spawn failures, non-zero exits, and wrapper reports become system messages
  in the channel — failures are visible where the work happens.
- On server start, runs marked running are reconciled against
  `tmux list-sessions`/`list-windows`; orphans are marked failed with a
  system note.
- `wt` absent → git fallback; both absent/broken → project is created with
  the main worktree only, with a visible warning.
- The server refuses to touch tmux sessions it did not create, except to
  open windows in an explicitly configured `attach_session`.

## Testing

- Unit: config merge, preset merge, command-template rendering, worktree
  JSON parsing, provider registry.
- Spawner: TmuxSpawner against a fake exec recorder (asserts the tmux
  commands issued), wrapper exit reporting against httptest server.
- HTTP handlers: httptest; SSE smoke test.
- One optional integration test against real tmux, tagged, skipped in CI by
  default.

## Development workflow rules (standing, from the user)

1. Always develop on a git worktree — never directly on main's checkout.
2. After finishing an implementation increment, build and provide the binary
   (absolute path) for the user's manual testing.
3. Do not merge to main; the user merges.
4. Never ask about pushing to remote, and never push.
5. Always reference files with absolute paths.

## Open items deliberately deferred to the implementation plan

- Exact chi vs stdlib routing decision at code time (either is fine; plan
  picks one and sticks to it).
- Junie/Kimi exact CLI invocations — confirmed against the installed
  binaries during implementation, since provider commands are config-level.
- Message rendering details (markdown subset) — plan phase.
