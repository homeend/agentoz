# Worktree creation from the sidebar — design

Date: 2026-09-10. Status: approved in chat, spec for review.

## Goal

A "+" next to each project name in the sidebar creates a git worktree for
that project without leaving erbrus: either check an existing local branch
out into a new worktree, or create a new branch from a base branch and the
worktree on it. Git work is delegated to `gg` (gigagit, `/mnt/t/others/gigagit`)
on the happy path only. When gg fails, erbrus shows gg's message and offers
a terminal in the main repository so the human finishes by hand.

Out of scope (decided in chat): remote-tracking branches as bases, naming
channels after branches (channels keep being named after the worktree
directory), branch creation without a worktree, worktree removal.

## Verified gg behaviour (2026-09-10, scratch repo, stdin closed, no TTY)

| command | effect | on failure |
|---|---|---|
| `gg worktree add --branch <name>` | checks the existing local branch out into a new worktree | `error: create worktree: no local branch "x"` / `error: create worktree: branch x is already checked out in worktree <path>`, exit 1 |
| `gg worktree add --from <base> <name>` | creates branch `<name>` at `<base>` and its worktree in one step | `error: create worktree: path already exists: <path>`, exit 1 |
| `gg branch ls` | local branches, one per line, `* ` marks HEAD, `↑a ↓b` suffix when an upstream exists | — |
| `gg worktree list` | `<branch>\t<path>` per line, main worktree first | — |

Success prints `→ creating worktree: <branch> → <path>` then
`✓ created worktree <branch> at <path>`, exit 0. The bare form
`gg worktree add <start-point>` is NOT "check out that branch": it creates a
new branch `<start-point>-<date>` from it, so erbrus never uses it.

Worktree location is gg's decision (`[worktree] path_template`, default
`../<repo>.worktrees/<branch>`), overridable per repo in `.gg.toml`. erbrus
never asks for or computes a path.

Forks that would prompt in a terminal error out non-interactively and ask
for a flag; erbrus passes none beyond the two above, so any such error is
shown to the human as is.

## Configuration

- New global key `gg_bin` (default `gg`), looked up on PATH like `wt_bin`;
  part of the live-reloaded set (`ReloadConfig`), no restart.
- `Provider.Type`: `agent` (default; every existing provider) or `tool`. A
  tool provider is a terminal program for the human, not a coding agent.
- Built-in registry entry `shell`: type `tool`, command `${SHELL:-bash}`
  (expanded by `cmd.sh` at exec time, so the user's login shell from the
  tmux server's environment wins), no prompt, no screen rules, no home
  directory or skill. `erbrus agents list` shows it with its type; `agents
  setup` seeds it like any other provider (no binary check: `$SHELL` always
  exists). Preset name `shell`.

## Tool runs

A run whose provider has type `tool` goes through `spawnRunCore` with these
differences, each keyed on the type, never on the provider name:

- no preamble, no handoff context, no provider hook args, no prompt
  (`--prompt` is ignored; the spawn form does not require one), no paste;
- the run row is created as today (status `starting` → `running`), the tmux
  window is named `<workdir>/<agent>` as today, and `ERBRUS_*` env vars are
  still set so `erbrus msg send` works from the shell if the human wants it;
- the screen watcher does not classify it: state stays a fixed `tool`
  (shown as "shell" on the card), no stall detection, no notifications;
- the composer's target list and the forward dialog's target list skip it;
  `sendToRun` refuses it with "<agent> is a shell, not an agent";
- it appears under "Agents in channel" with a `shell` badge instead of a
  state badge, can be stopped there, and opens the same terminal page.

## The "+" and the form

Sidebar: every `<div class="proj">` gets a trailing `+` link (title "new
worktree") to `GET /ui/projects/{id}/worktree`. Archived projects have no
sidebar entry today; nothing changes there.

Page "New worktree for <project>" (`worktree.html`, same chrome as spawn):

- mode radio: **Existing branch** / **New branch** (default: new branch);
- existing: `<select name="branch">` of local branches; a branch already
  checked out (main worktree or any linked worktree, from `gg worktree
  list`) is listed disabled with "(checked out in <dir>)";
- new: `<input name="name">` (required, no whitespace) and
  `<select name="base">` of local branches, default = the current branch of
  the main worktree;
- buttons: Create, Cancel (back to the project's main channel).

Branch data comes from `gg branch ls` and `gg worktree list` at render time,
run in the project's repo root. If either fails the page renders with the
error and the terminal button (see Outcomes) instead of the form.

`POST /ui/projects/{id}/worktree` with `mode`, `branch` | `name`+`base`:

- validation errors re-render at 422 with the message above the form;
- runs gg in the repo root: `gg worktree add --branch <branch>` or
  `gg worktree add --from <base> <name>`, stdin closed, 60 s timeout,
  output captured;
- success: `syncChannels(project)` (the existing add/archive/restore pass)
  creates the channel named after the new directory; the redirect goes to
  that channel, found by matching the path from gg's `created worktree …
  at <path>` line (fallback: `wt.List` by branch). If the sync fails after
  gg succeeded, redirect to `/ui/projects?warning=worktree created at
  <path>, but the channel sync failed: <err>` — the worktree is never
  removed by erbrus;
- failure: re-render at 422 with gg's message (the last `error: …` line,
  else the trimmed combined output, else the exit error) and the terminal
  button; a missing binary reads `gg not found (gg_bin = "gg"): install
  gg or set gg_bin in config.yaml`.

## The terminal button

"Open a terminal in <repo root>" is a form `POST /ui/projects/{id}/shell`.
It spawns a `shell` tool run in the project's main channel (the channel
whose worktree path is empty — `general`) with workdir = repo root, then
redirects to `/ui/runs/{run}/terminal`. It is a plain spawn request through
`spawnRunCore` with preset `shell`; nothing else is special. The button
also appears on the form page when branch listing failed, and the same
route can be used from anywhere later (not wired elsewhere now).

## Code shape

- `internal/gg`: `Runner` (same signature as `wt.Runner`), `Branches(run,
  bin, root) ([]Branch, error)` with `Branch{Name, Current, Ahead, Behind}`,
  `Worktrees(run, bin, root) ([]Worktree{Branch, Path}, error)`,
  `AddForBranch(run, bin, root, branch) (Created, error)`, `AddFrom(run, bin,
  root, base, name) (Created, error)` with `Created{Branch, Path}`, and
  `Message(err, output) string` for the error extraction. The runner is
  injected so tests use recorded gg output; the real runner sets
  `cmd.Stdin = nil` (closed), a context timeout, and merges stderr into the
  captured output.
- `internal/config`: `GgBin`, `Provider.Type` + validation (`agent`|`tool`|
  empty), live reload of `GgBin`.
- `internal/agents`: the `shell` entry; `agents list` gains a type column.
- `internal/server`: `ui_worktree.go` (form GET/POST, shell POST),
  `runs.go` tool branch, `watch.go` skip for tool runs, `ui_channel.go` /
  `ui_forward.go` target filters + `sendToRun` refusal, sidebar data
  (project id already present), `ggBin()` snapshot accessor.
- `internal/web`: `worktree.html`, sidebar `+` in `channel.html` (the
  sidebar partial), `shell` badge in `_runs.html`, minimal CSS.

## Testing

- `internal/gg`: parsers against the recorded outputs in the table above
  (branch list with `* ` and `↑1 ↓2`, worktree list, success lines, all
  three error forms, missing binary).
- Server, fake runner: form renders branches with checked-out ones
  disabled; validation 422s; success runs the right gg command in the repo
  root, syncs, redirects to the new channel; gg failure 422 carries the
  message and the terminal button; sync failure after success redirects
  with the warning; terminal button spawns a shell run in the main channel
  and redirects to its terminal; tool run has no preamble/hook/paste, is
  absent from composer and forward targets, is refused by `sendToRun`, is
  not classified by the watcher.
- Config: `type` validation, `gg_bin` reload.
- Live: one pass on the throwaway instance (ERBRUS_URL=http://127.0.0.1:7499)
  with the real gg on a scratch repo: both modes, one failure, the terminal.
