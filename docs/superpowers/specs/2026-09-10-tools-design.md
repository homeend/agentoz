# Tools: open a shell, gg, or an editor in a channel's directory — design

Date: 2026-09-10. Status: approved in chat, spec for review.

## Goal

One click on the channel page opens a tool in that channel's directory
(the worktree, or the repo root for the main channel), so the human can
inspect an agent's changes where they live. Tools are user-defined in
config: terminal programs (a shell, `gg`) run in a tmux window and open in
the web terminal; GUI programs (IntelliJ, Sublime, Notepad) are launched
as detached processes with the directory as their argument.

Decided in chat: tools live in their own config section (not providers),
hot-reloaded; buttons sit under Presets on the channel page; GUI tools
report only launch failures; gg takes no flag; editors may be Windows
binaries while erbrus runs on WSL (paths need translating) or erbrus
itself may run as a Windows binary.

## Configuration

```yaml
tools:
  shell: {command: "${SHELL:-bash}", terminal: true}   # built-in
  gg:    {command: "{gg_bin}", terminal: true}         # built-in
  idea:  {command: "idea64.exe {windir}"}
  subl:  {command: "subl.exe {windir}"}
```

- `tools` is a map name → `{command, terminal}`. `command` is a shell
  template; `terminal: true` means the program needs a TTY.
- Built-ins `shell` and `gg` are merged into every loaded config
  (`Defaults()` and `LoadGlobal`); a user entry of the same name wins.
- Placeholders, substituted before shell quoting of the VALUE (the
  template itself is the user's shell text and is not quoted):
  - `{dir}` — the channel directory as erbrus sees it;
  - `{windir}` — its Windows form. On Linux/WSL: `/mnt/<x>/rest` →
    `X:\rest` (backslashes); any other absolute path →
    `\\wsl.localhost\<$WSL_DISTRO_NAME>\rest` when that variable is set,
    else the path unchanged. On a Windows build: `{dir}` unchanged.
  - `{gg_bin}` — the config value (default `gg`).
  - An unknown `{name}` is an error reported at launch ("unknown
    placeholder {name} in tools.<tool>.command").
- The section is part of the hot-reloaded set (`ReloadConfig`), with a
  `toolsSnapshot()` accessor like providers.
- The round-5 built-in `shell` provider and `shell` preset are removed:
  `tools.shell` replaces them. `Provider.Type` stays as a documented,
  user-settable field; nothing built-in uses it any more.

## The rail

On the channel page, a third collapsible section `data-rail="tools"`
under Presets, open by default (remembered per browser like the others):
one row per tool, sorted by name, name + "Open" button.

- Terminal tools: `<form method="post" action="/ui/channels/{id}/tools/{name}" target="tool:{name}:{id}">` — the response is a redirect to the
  run's terminal page, so it opens (or focuses) a separate tab, as the
  Screen button does.
- GUI tools: the same form without a target; success redirects back to
  the channel with no note; failure redirects with `?warning=`.
- Archived channels show no Tools section (their directory is gone).

## Terminal tools

`POST /ui/channels/{id}/tools/{name}` with `tools[name].terminal`:

- Spawns a tool run through `spawnRunCore` with a new request field
  `Tool string`. With `Tool` set, provider resolution is skipped: the
  command is the rendered tool template, `run.Provider = "tool:<name>"`,
  `run.AgentName = <name>`, workdir = the channel directory (`Workdir`
  override ignored), no preamble/prompt/paste/hook, env as for any run.
- `isToolRun` is true for `Provider` starting with `tool:` (and still
  for a provider of type `tool`). The watcher skips it; the card shows
  the `shell` badge (label text: "tool"); it is not a composer or forward
  target; `sendToRun` refuses it.
- The response redirects to `/ui/runs/{run}/terminal`.
- Without a spawner (Windows build, or `serve` without tmux) the launch
  fails with "terminal tools need tmux (not available here)".
- The worktree page's "Open a terminal in <repo>" button posts to
  `/ui/channels/{main}/tools/shell` (same handler); the standalone
  `/ui/projects/{id}/shell` route from round 5 is removed.

## GUI tools

`POST /ui/channels/{id}/tools/{name}` without `terminal`:

- The rendered command runs through the platform shell (`sh -c` on
  Linux, `cmd /C` on Windows) with `Dir` = the channel directory, stdin
  closed, stdout/stderr discarded, started detached (`Setsid` on Linux;
  `CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS` on Windows) and released.
- A start error (shell missing, directory missing) is the only failure
  reported: redirect to the channel with `?warning=<tool>: <error>`.
  A program that starts and then exits non-zero is not observed — the
  user asked for failure messages only, and an editor outlives erbrus.
- Nothing is recorded: no run row, no system note.
- The launcher is an interface on the server (`Launcher.Start(dir,
  command string) error`) so tests use a fake; the real one is
  `exec.Command`-based, per-OS files for the detach flags.

## Config surfaces

- Settings page template comment gains a `tools:` example.
- `erbrus tool list` (CLI, optional, small): prints the merged tools
  with their commands and kind — useful to check placeholders. Not
  required for the feature; include only if it stays under ~40 lines.

## Testing

- config: defaults carry `shell`+`gg`; user override wins; merge after
  load; reload swaps tools (`toolsSnapshot`).
- render: `{dir}`, `{windir}` (WSL `/mnt/t/x` → `T:\x`; `/home/u/p` with
  `WSL_DISTRO_NAME=Ubuntu` → `\\wsl.localhost\Ubuntu\home\u\p`; without
  the variable unchanged; Windows build unchanged), `{gg_bin}`, unknown
  placeholder error, `${SHELL:-bash}` untouched.
- server, terminal tool: POST spawns a run with provider `tool:gg`,
  agent name `gg`, workdir = channel dir, cmd.sh `exec <rendered>`, no
  preamble, redirect to the terminal page; the run renders with the tool
  badge and is absent from composer/forward targets; no spawner → 422
  banner text "needs tmux".
- server, GUI tool: fake launcher records (dir, command); success
  redirects to the channel without a note; failure redirects with the
  warning; unknown tool → 404; archived channel → no rail section and
  the POST is refused.
- worktree page: the terminal button posts to the main channel's shell
  tool and lands on a terminal page.
- Live on the throwaway (ERBRUS_URL set): `gg` opens in tmux in a
  worktree channel's directory (capture shows the gg TUI); a GUI tool
  defined as `touch {dir}/opened.txt` writes the marker; a tool with a
  missing binary shows the banner.

## Amendments (implementation, 2026-09-10)

- **GUI tools run without a shell.** The spec said `sh -c` / `cmd /C`.
  With a shell in between, a missing editor binary is not a start error
  (the shell starts fine and fails inside), so the one failure that
  matters would never be reported. The rendered command is split into
  argv (`tools.Split`: whitespace, single quotes literal, double quotes
  with `\"` and `\\`) and executed directly; a missing binary reports
  "executable file not found". Consequence: no `$VAR`, redirects or
  pipes in GUI tool commands — quote arguments with spaces instead.
  Terminal tools are unaffected (cmd.sh is a shell script).
- **Foreground + tool** (`erbrus start --fg` shape) is refused with 400;
  tools are tmux-only.
- **Tool runs reset model/args too**, not only prompt/workdir/name, so a
  stray API request cannot append arguments to a tool command.
- `ExecLauncher()` is exported for `serve`; the badge on a tool run's
  card now reads "tool" (was "shell").
- The optional `erbrus tool list` CLI was not built (YAGNI); the
  settings page's scaffold comment documents the section instead.
