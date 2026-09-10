# Platform driver: tmux on Linux, WezTerm on Windows — design

Date: 2026-09-10. Status: approved in chat (visible WezTerm window for terminal tools on Windows accepted).

## Goal

`erbrus.exe serve` on Windows can spawn, watch, talk to and stop agent
runs, the way the Linux build does through tmux. The multiplexer and the
host shell are hidden behind ONE abstraction, `spawn.Driver`: the server,
the watcher, the chat delivery path and the Tools rail talk to the driver
and never to tmux, WezTerm, `sh` or PowerShell by name.

WezTerm is the Windows multiplexer: its headless mux server runs natively
on ConPTY, so Node and JVM agents (Claude Code, Codex, Junie) get a real
console, and `wezterm cli` covers every spawner operation.

Out of scope: the browser terminal on Windows (no pty there; the Screen
page with the keypad stays), agents without a Windows CLI, macOS (the
driver would work there but nothing is verified).

## Two axes, one driver

What differs between the Linux and Windows builds splits cleanly:

| axis | chosen by | what it decides |
|---|---|---|
| **multiplexer** (tmux, wezterm) | config `terminal:`; default tmux on Linux, wezterm on Windows | spawn / stop / alive / capture / send / keys; window naming; stale-view sweep; whether a browser terminal exists; how "open a shell here" works |
| **host** (posix, windows) | `runtime.GOOS` | the command file a run executes (`cmd.sh` / `cmd.json`), whether the prompt may go on the command line, how agents spell the erbrus path, the built-in `shell` tool, `{windir}` |

`spawn.Driver` bundles both. The server gets a driver from
`spawn.NewDriver(cfg)`; tests get one from `spawn.DriverFor(fakeSpawner)`
with posix host defaults, so the fake spawners in the server tests keep
working unchanged.

```go
// internal/spawn/driver.go
type Driver interface {
    Spawner                                  // Spawn, Stop, Alive, Send, Capture, SendKeys
    Name() string                            // "tmux" | "wezterm", stored in run.Spawner
    // Host shell.
    WriteCommand(runDir, command string) (cmdFile string, err error)
                                             // cmd.sh (exec …) or cmd.json (argv, no shell)
    PromptByPaste() bool                     // Windows: the prompt never goes on the command line
    AgentBin(bin string) string              // the erbrus path as agents should type it
    ShellTool() string                       // built-in `shell` tool template
    // Multiplexer extras.
    Viewer() (Viewer, bool)                  // browser-terminal support (tmux only)
    Sweep() []string                         // kill stale view sessions at startup
    // OpenTerminal says how a terminal tool opens in dir. ok=false: make a
    // tool run (tmux window + browser terminal). ok=true: launch argv
    // detached through the GUI Launcher (a visible WezTerm window).
    OpenTerminal(dir string, argv []string) (launch []string, ok bool)
}
```

Implementation: `driver` struct = `Spawner` + `host` + the mux extras.
`host` is a tiny interface (`WriteCommand`, `PromptByPaste`, `AgentBin`,
`ShellTool`) with two values, `posixHost` and `windowsHost`, chosen by GOOS
in `NewDriver`.
The mux extras come from the concrete spawner (`*Tmux` has `SweepViews`
and is a `Viewer`; `*Wezterm` has neither and answers `OpenTerminal` with
`wezterm start --cwd dir -- argv…`).

What the rest of the code stops knowing:

- `runs.go`: `spawnerKind := "tmux"` → `driver.Name()`; the `#!/bin/sh
  exec` file → `driver.WriteCommand`; `pasteMode(provider) ||
  driver.PromptByPaste()`; every place the erbrus path is written into
  text an agent will type (`Preamble`, `ChatEnvelope`, `ForwardToAgent`,
  `ProviderHook`) gets `driver.AgentBin(s.erbrusBin)`.
- `Reconcile` and the watcher select runs by `TmuxTarget != ""`, not
  `Spawner == "tmux"`. The column keeps its name (a rename touches the
  schema for nothing).
- `serve.go`: `spawn.NewDriver(cfg)`; `driver.Sweep()`.
- `screen.go`: `driver.Viewer()` instead of the type assertion.
- `ui_tools.go`: terminal tools ask `driver.OpenTerminal`.
- `config.BuiltinTools()` takes the shell template from the driver.
- `erbrus wrap <cmdfile>` picks the runner by extension: `.sh` → `sh
  cmd.sh`; `.json` → the argv array executed directly, no shell. Not a
  driver method: `wrap` runs inside the window, far from the server.

## Verified `wezterm cli` behaviour (2026-09-10, Windows 11, WezTerm in
`C:\Users\homee\bin\WezTerm`, no GUI running)

| command | effect | output |
|---|---|---|
| any `wezterm cli …` with no server | starts `wezterm-mux-server --daemonize` itself | two `WARN` lines on stderr, then the result |
| `spawn --new-window --cwd <dir> -- <prog…>` | new mux window+tab+pane running prog in dir | the pane id, one integer on stdout |
| `spawn` without `--new-window` | error: no focused pane headless | `ERROR … --pane-id was not specified …`, exit 1 |
| `set-tab-title --pane-id N "<title>"` | names the tab | nothing |
| `list --format json` | every pane: `pane_id`, `tab_title`, `title`, `cwd` (`file:///T:/others/x/`), `size.rows/cols`, `cursor_*`, `workspace` | JSON array |
| `send-text --pane-id N --no-paste "<text>"` | types text raw | nothing |
| `send-text --pane-id N "<text>"` | text in a bracketed paste when the program enabled it | nothing |
| `get-text --pane-id N` | the visible screen, plain text, one line per row, trailing blank rows dropped | text |

Not yet verified (the paste stopped short): `kill-pane --pane-id N`, and
`get-text` on a pane that is gone. Assumed: kill-pane removes the pane
from `list`; get-text on an unknown pane exits non-zero. Both get checked
live first thing and the recorded outputs go into the tests.

Gaps versus tmux, and what the design does about them:

- **No environment flag on `spawn`.** tmux gets `-e K=V`; wezterm has
  none. The run's env moves into a file (see Env file).
- **No `remain-on-exit`.** The pane closes when the program exits, so
  the last screen is gone. `erbrus wrap` reports the exit code to the
  server, so status stays right; only the final screen is lost.
- **No activity timestamp.** `Screen.Activity` stays zero; the watcher
  already treats zero as unknown (no stall detection).
- **No pty attach.** No browser terminal; `Viewer()` answers false.
- **`send-text` takes the text as an argument or on stdin.** Always
  stdin: no 32 K command-line limit, no escaping. The wezterm spawner's
  runner therefore takes stdin: `type StdinRunner func(stdin string,
  name string, args ...string) ([]byte, error)`.

## Configuration

```yaml
terminal: wezterm        # default on Windows; tmux elsewhere
wezterm_bin: wezterm     # looked up on PATH, like wt_bin / gg_bin
```

- `terminal` already exists. Values `tmux` | `wezterm`; anything else is
  a startup error. Empty = the platform default.
- `wezterm_bin` defaults to `wezterm`, read at startup (the driver is
  built once, like tmux today).
- `session_pattern` (`erbrus-{project}`), `session` and `attach_session`
  name the WezTerm **workspace** (`spawn --workspace`), so `wezterm
  connect unix` shows one workspace per project. `attach_session` has no
  "must exist" check on wezterm (workspaces are created on use).

## The wezterm spawner

`internal/spawn/wezterm.go`, `NewWezterm(run StdinRunner, bin string)`.
Handle = `<pane id>@<tab title>` (stored in `run.TmuxTarget`). Pane ids
restart at 0 whenever the mux server restarts (reboot, or the user killing
it), so a stored id alone could name somebody else's fresh pane; every
lookup matches BOTH `pane_id` and `tab_title` in `list --format json`, and
a run whose pair is gone counts as exited.

| method | wezterm cli |
|---|---|
| `Spawn` | `spawn --new-window --workspace <session> --cwd <workdir> -- <command…>` → pane id; then `set-tab-title --pane-id N <WindowName>` (failure ignored, cosmetic) |
| `Alive` | `list --format json`; true when a pane with that id AND tab title exists; a failed list is an error |
| `Capture` | `get-text --escapes --pane-id N` (colour parity with `capture-pane -e`; `frameOf` strips before classifying) → `Screen{Raw}` plus `Cols`/`Rows` from the pane's `size` in `list`; error when the pane is gone |
| `Send` | `send-text --pane-id N` with the text on **stdin** (paste mode; no argv length or escaping questions), wait `sendSettle`, `send-text --no-paste` with `\r` on stdin, then the shared submit check |
| `SendKeys` | `send-text --no-paste` with the key translated: Enter `\r`, Escape `\x1b`, Up `\x1b[A`, Down `\x1b[B`, Tab `\t`, a single character as is; any other name → "key not supported" |
| `Stop` | `kill-pane --pane-id N` |

`SendChecked` (settle, retries, `pendingInInputBox`, the provider's own
submitted rule) moves from a `*Tmux` method into a package function
`sendChecked(capture, paste, enter func…, text, submitted)` that both
spawners call. No behaviour change for tmux.

stderr is never parsed (the mux auto-start warnings live there);
`CmdRunner` returns stdout. Errors carry the trimmed
`exec.ExitError.Stderr`, as `gg.Message` does.

## Env file

`spawnRunCore` writes `<runDir>/env` (one `KEY=VALUE` per line) next to
the command file, on every platform. `erbrus wrap <cmdfile>` loads
`<dir of cmdfile>/env` into the child's environment when present, on top
of its own. tmux keeps `-e` too: a human in a shell tool run then has
`ERBRUS_*` in the window's own shell, which the file cannot give. The fg
path is unchanged.

## The command file on Windows: argv, no shell, prompt by paste

`posixHost.WriteCommand` writes `cmd.sh` = `#!/bin/sh\nexec <command>`
(as today). `windowsHost.WriteCommand` runs the rendered command through
`tools.Split` (the shell-words splitter from round 6: single quotes
literal, double quotes with `\"`) and writes `cmd.json`, a JSON array;
`wrap` executes it with `exec.Command(argv[0], argv[1:]...)`. No
PowerShell and no cmd.exe in the path, for two reasons found while
designing:

- Windows PowerShell 5.1 (what bare `powershell` is) mangles arguments
  to native programs when they contain embedded double quotes or
  newlines; the fix exists only in PowerShell 7.3+.
- Go's `os/exec` quotes for `CommandLineToArgvW`, and documents that
  cmd.exe "and thus all batch files" unquote differently. An npm-installed
  `claude` on Windows is a `claude.cmd` shim, and cmd.exe reads one line:
  a newline inside an argument truncates the command.

Because of the second point the **prompt never goes on the command line
on Windows**: `PromptByPaste()` is true there, so every provider behaves
like junie/antigravity today (the CLI starts bare, `deliverPrompt` types
the preamble once the input box is up, then Enter). What remains on argv
are flags, model names and paths, which survive a `.cmd` shim.

Quoting stays POSIX everywhere (`provider.ShellQuote`, `tools.shellQuote`
unchanged): `Split` understands it, so the same rendered text feeds `sh`
on Linux and the argv file on Windows. Provider templates in config stay
as they are.

`AgentBin`: on Windows `filepath.ToSlash(bin)`. Claude Code on native
Windows runs shell commands through Git Bash, where `T:\others\erbrus\bin
\erbrus.exe msg send` loses every backslash; `T:/others/erbrus/bin/erbrus.exe`
works in Git Bash, cmd and PowerShell alike. Linux: unchanged.

`ShellTool()`: `${SHELL:-bash}` on posix, `powershell` on Windows. A user
template with `${SHELL:-bash}` on Windows is the user's problem; the
scaffold says so.

## Terminal tools on Windows

The Tools rail keeps one handler. It asks `driver.OpenTerminal(dir,
argv)`: tmux says no → tool run + browser terminal (today's path);
wezterm says `wezterm start --cwd dir -- argv…` → launched detached
through the GUI `Launcher`, a visible WezTerm window in that directory.
No run row, no `target` on the form, only a start error is shown, like a
GUI tool. (Assumption: the chat question about this got no answer; the
design leans this way.)

## Testing

- `internal/spawn/wezterm_test.go`, recorder runner as in `tmux_test.go`:
  Spawn issues `spawn --new-window --workspace … --cwd … -- …` then
  `set-tab-title` and returns the pane id; Alive true/false from recorded
  JSON; Capture returns the recorded screen with size; Send pastes, sends
  `\r`, retries when the recorded screen still shows the text; SendKeys
  translations and the unsupported-key error; Stop calls kill-pane;
  stderr text ends up in the error.
- `internal/spawn/driver_test.go`: `NewDriver` picks tmux/posix on linux
  and wezterm/windows on windows (GOOS injected), rejects an unknown
  `terminal:`; `WriteCommand` file names and contents (`cmd.json` argv
  from a command with a single-quoted path containing a space);
  `PromptByPaste`, `AgentBin` (`T:\\x\\erbrus.exe` → `T:/x/erbrus.exe`),
  `OpenTerminal` answers.
- `internal/cli`: `wrap` loads the env file (child prints
  `ERBRUS_RUN_ID`); missing file is fine; `.json` runs the argv directly
  (a child that echoes its arguments, including one with a space).
- `internal/server`: `Reconcile` and the watcher pick up a run whose
  `Spawner` is `wezterm`; spawn writes the env file; the command file
  comes from the driver (a fake driver with a `.json` host proves nothing
  else assumes `cmd.sh`); a `PromptByPaste` driver puts no prompt on the
  command line for claude-code; the preamble carries the `AgentBin` form;
  the Tools rail on an `OpenTerminal`-true driver launches and records no
  run.
- `./test.sh -cross` stays green.
- Live, on the user's Windows machine (this session cannot run Windows
  programs: WSL interop is off): `build.cmd`, `erbrus serve` on 7421,
  spawn Claude Code into a channel, watch the Screen page classify it,
  send a chat message, receive the report, stop it; open `shell` from the
  Tools rail. Outputs pasted back into the chat.

## Non-goals, stated

- Keeping the last screen after exit (would need `exit_behavior = "Hold"`
  in the mux server's config and a dead-pane heuristic).
- A browser terminal on Windows (would need a ConPTY host inside erbrus;
  the "erbrus hosts the ptys itself" option from the chat).
- Zellij.

## Amendments (implementation, 2026-09-10)

- **`*driver` forwards `SendChecked` to the underlying spawner.** The
  server's `checkedSender` type assertion (`deliver.go`) looks for
  `SendChecked` on the dynamic type behind `s.spawner`; once `SetDriver`
  makes that dynamic type always `*spawn.driver`, embedding `Spawner` as
  an interface field only promotes the interface's own methods, not the
  wrapped concrete spawner's extras. Without a forwarding method on
  `*driver`, both tmux and wezterm silently fell back to plain `Send`,
  losing each provider's own "submitted" screen rule (agy keeping text
  in its input box while working, for one). Purely additive on
  `internal/spawn/driver.go`, not on the `Driver` interface itself (so
  test fakes need not implement it).
- **`Server.drv()` returns `spawn.DriverFor(nil)` when no driver is
  set.** Collapses every scattered `if s.driver != nil { … } else { … }`
  across `server.go`/`runs.go`/`screen.go`/`terminal.go`/`ui_tools.go`
  into one accessor, and removes the last `"tmux"`/`sh` string literals
  duplicated outside `internal/spawn`. `DriverFor(nil)`'s host methods
  never touch the nil `Spawner`; `Viewer()`/`OpenTerminal()` type-assert
  it and get `ok=false`, not a panic — so the fg-only test configuration
  never names `sh`/tmux itself, it just gets the posix defaults for
  free.
- **`agent_runs.spawner` CHECK constraint now includes `'wezterm'`.**
  `store.CreateRun` rejected `Spawner: "wezterm"` with a 500 under the
  original `CHECK (spawner IN ('tmux','fg'))`. Existing on-disk
  databases keep the old constraint baked into `sqlite_master` (SQLite
  can't `ALTER` a CHECK in place), so `store.migrateSpawnerCheck` rebuilds
  `agent_runs` via copy/drop/rename when its stored DDL doesn't mention
  `wezterm` — the replacement DDL is sliced out of the embedded
  `schema.sql` itself, never re-typed, so the rebuilt table's columns
  can't drift from the schema. A no-op on every fresh database.
- **The built-in `shell` tool's command is replaced by `driver.ShellTool()`
  on `SetDriver` and on every reload, only when the config's entry
  still equals the built-in.** So a wezterm/Windows run gets `powershell`
  without the user having to say so, but a user-defined `shell` tool is
  never clobbered. Applied to the freshly loaded tools *before* the
  reload's `reflect.DeepEqual` "did anything change" comparison, not
  after swapping — applying it after would leave `s.cfg.Tools` forever
  out of sync with every subsequent `next.Tools` on a non-posix driver,
  so an unchanged config file would report `changed=true` on every
  single reload.
- **Terminal tools: `driverOpensTerminals()` is checked first.** Argv is
  only split and rendered on the launch path (`driver.OpenTerminal`);
  the tmux path goes straight to `spawnRunCore` exactly as before, so a
  bad tool command still fails inside the tmux pane, not at this
  handler — the tmux path's error behaviour is unchanged.
- **`Defaults().Terminal` is empty**, not `"tmux"`; the platform default
  is applied once, by `spawn.NewDriver`, so the zero value never lies
  about what a Windows build actually picks. `wezterm_bin` added next
  to `gg_bin`, defaulting to `wezterm` on PATH.
- **The mock-key fix in the wezterm spawn test.** The brief's verbatim
  `TestWeztermSpawnUsesAttachSessionAsWorkspaceAndReportsStderr` scripted
  its recorder under the key `"wezterm cli spawn"`, but `cli()` invokes
  the *configured* binary as `name` (required so a non-PATH
  `wezterm.exe` install actually gets called) — with a custom bin path
  the recorded call never has that prefix. Fixed by keying the mock to
  the full configured path (`` `C:\Users\homee\bin\WezTerm\wezterm.exe cli spawn` ``);
  no assertion in the test changed.
- Known gaps carried to `docs/BACKLOG.md`: the Windows live check (this
  session cannot run Windows programs); keeping the last screen after
  exit; a browser terminal on Windows; user-facing "tmux" wording on
  the wezterm path; `shellToolOverride` non-idempotence across driver
  switches; the `s.driver` read in `ReloadConfig` racing the unlocked
  write in `SetDriver`; `wrap`'s `os.Setenv` leaking env-file
  keys into the process; a malformed `cmd.json`/env file exiting 127
  without reporting the exit; small test-coverage gaps.
