# erbrus — interactive web terminal (live screen round 3) design

Date: 2026-09-08. Status: approach 2 ("in-process") chosen in chat; spec
awaiting review. Work happens on branch `worktree-web-terminal` in
`/mnt/t/others/erbrus/.claude/worktrees/web-terminal` because the feature is
large and the approach may still change.

Predecessors:
- /mnt/t/others/erbrus/docs/superpowers/specs/2026-09-08-live-screen-design.md (round 1, read-only screen)
- /mnt/t/others/erbrus/docs/superpowers/specs/2026-09-08-screen-state-design.md (round 2, state detection, keypad)

## Purpose

Let a human type into an agent's tmux window from the browser with full
terminal fidelity: every key, colors, cursor, redraws, exactly what a
terminal attached to that window shows. Today the screen page is a
read-only rendering polled from `capture-pane`, with a keypad for dialog
answers and the channel composer for text. Round 3 replaces "look, then go
to your terminal to type" with a real terminal in the page.

## Investigation record (2026-09-08)

Kept here as the feature's reference; the decisions below rest on it.

### The constraint that shaped the design

erbrus must never disturb the user's live tmux terminal. The user's server
runs `window-size latest` (checked with `tmux show-options -g`), which
means the most recently active client dictates every window's size. A
naive browser client attaching at its own size reflowed the user's window
(probe: 100x30 became 40x10 as soon as a second 40x10 client attached, and
a grouped session did not help by itself).

Three tmux features, all verified on tmux 3.7c in throwaway sessions
(`claudetest-*`), remove the problem:

| Feature | Command | Verified effect |
|---|---|---|
| Grouped session | `tmux new-session -t <session> -s <view>` | Shares the windows but has its own current window: selecting window 2 in the view left the user's session on window 1. |
| Size-neutral client | `-f ignore-size` on `new-session`/`attach` | A 60x15 pty client and a 40x10 control-mode client viewing a 120x35 / 100x30 window left it at 120x35 / 100x30. `list-clients` shows the flag. |
| Self-cleanup | `set -t <view> destroy-unattached on` | The view session disappeared within 0.5 s of the pty client closing. |

Also verified through a real pty client (Python `pty.fork`, the same shape
a Go server uses): the client received the rendered window (810 bytes with
the spinner line and prompt glyph), bytes written to the pty arrived in the
window (`capture-pane` showed them), and tmux options `status off` on the
view session did not touch the user's session.

Other facts from the probes:

- `send-keys -l -- <text>` types literal text safely even when it starts
  with a dash (kept for the keypad/composer paths, unchanged here).
- `#{cursor_x} #{cursor_y} #{cursor_flag}` are available but unneeded
  once a real terminal renders tmux's own cursor sequences.
- One `capture-pane` plus `display` pair costs about 6 ms.
- Control mode (`tmux -C`) emits `%output` per pane in real time. Not
  used: it delivers raw pane bytes that would need seeding and per-pane
  reassembly; a pty client gets tmux's own rendering for free.
- A headless-browser screenshot of ttyd came back blank because its xterm
  renderer needs WebGL and the headless page closed the socket after
  80 ms. Test rendering with a real browser or the pty-level checks.
- The user's tmux base-index is 1; never assume window 0 in tests.
- Copy mode is a property of the pane, shared by every session that shows
  it. A browser viewer scrolling into copy mode would freeze the pane in
  the user's terminal too and make `send-keys` land in copy mode. Hence
  no `mouse on` and no prefix on the view session (see Out of scope).

### Candidates surveyed

| Candidate | What | Status (fetched 2026-09-08) | Verdict |
|---|---|---|---|
| ttyd | C web terminal (xterm.js + websocket), installed here via linuxbrew (1.7.7) | 12.3k stars, release 2024-03, repo active 2026-08, MIT | Fastest sidecar; rejected because erbrus should stay a single binary |
| gotty | Same idea in Go, CLI only | 2.5k stars, v1.8.0 2026-05, MIT | Not embeddable; superseded by ttyd as sidecar |
| coder/websocket | Go websocket, net/http `Accept`, zero deps | 5.5k stars, v1.8.15 2026-06, ISC | **Chosen** |
| creack/pty | Go pty start/resize, Windows stub file present | 2.1k stars, v1.1.24 2024-10, pushed 2026-06, MIT | **Chosen** |
| @xterm/xterm 6.0.0 | Browser terminal widget | pushed 2026-09, MIT | **Chosen**, vendored |
| @xterm/addon-fit 0.11.0 | Fit terminal to container | MIT | Not needed: the terminal is sized to the tmux window, not the container |
| chrismccord/webtmux | Go + gorilla + xterm.js app, gotty fork | 137 stars, pushed 2026-01, MIT | Reference only |
| adrianleb/dsh-tmux-cc | TypeScript control-mode cockpit | 0 stars | Source of the `ignore-size` idea; not reusable |
| owenthereal/tmux and other Go tmux wrappers | Command wrappers | 2 stars, no control mode | Skip; erbrus already shells out |

### Approaches considered

1. **Key forwarding over the existing capture/SSE page** (no deps): browser
   key events mapped to tmux key names, 100 ms polling, cursor overlay.
   Was the recommendation while attaching looked unsafe. No mouse, no
   scrollback, snapshot latency. Dropped once `ignore-size` proved safe.
2. **ttyd sidecar**: one ttyd process per screen page running the grouped
   `ignore-size` client, embedded in an iframe. Half a day, but an
   external binary and per-page process/port bookkeeping.
3. **In-process** (chosen): coder/websocket + creack/pty + vendored
   xterm.js; the same tmux client command runs in a pty owned by erbrus,
   one websocket handler relays bytes. Two small Go deps, about 350 KB of
   vendored JavaScript, single binary, erbrus-styled page.

## Scope

In:

- A live xterm.js terminal on the existing screen page for running tmux
  runs, sized to the tmux window, input and output over one websocket.
- A pty-backed tmux view client per open terminal, grouped, size-neutral,
  self-destroying, with no prefix key and no status line.
- Vendored xterm.js under the embedded static tree with a pinned version
  and a refresh script.
- Fallback to the round 1/2 read-only page when the terminal cannot start.
- Startup sweep of stale view sessions.

Out (see the last section for reasons): mouse and scrollback, resizing
the agent's window from the browser, terminals for non-tmux spawners,
Windows hosts, authentication changes, persisting anything.

## Architecture

```
browser                        erbrus                          tmux server
xterm.js  <-- ws binary -->  handler  <-- pty -->  tmux new-session -t <sess> -s erbrus-view-<run>-<nonce>
  |                             |                    -f ignore-size ; status off ; destroy-unattached on ;
  |  text frame {cols,rows}     | size poll 2 s      prefix None ; select-window <target>
  '----------------------------'
state header + keypad  <-- SSE /screen/events?html=0 (unchanged feed, html omitted)
```

The user's own terminal keeps rendering the same window through its own
client. erbrus's view client shares the window but never contributes to
its size and never changes the user's current window.

## Components

### `internal/spawn` — the view command

The `Spawner` interface stays as it is. A new optional interface is
satisfied by `*Tmux` only:

```go
// Viewer is implemented by spawners whose windows can be attached to
// interactively. The server type-asserts it; other spawners get the
// read-only page.
type Viewer interface {
    // ViewCommand returns the argv of a client that shows h at the
    // window's own size without affecting other clients, in a session
    // named view. It is run in a pty by internal/termview.
    ViewCommand(h Handle, view string) ([]string, error)
    // Size reports the window's current columns and rows.
    Size(h Handle) (cols, rows int, err error)
}
```

`Tmux.ViewCommand("erbrus-web:7", "erbrus-view-12-a1b2c3d4")` returns
exactly:

```
tmux new-session -t erbrus-web -s erbrus-view-12-a1b2c3d4 -f ignore-size
  \; set -t erbrus-view-12-a1b2c3d4 status off
  \; set -t erbrus-view-12-a1b2c3d4 destroy-unattached on
  \; set -t erbrus-view-12-a1b2c3d4 prefix None
  \; set -t erbrus-view-12-a1b2c3d4 prefix2 None
  \; select-window -t =erbrus-view-12-a1b2c3d4:7
```

(as one argv, `;` as separate arguments). A malformed handle (no colon)
is an error. `Size` runs `tmux display -p -t =h '#{window_width} #{window_height}'`.

`ViewName(runID int64) string` returns `erbrus-view-<runID>-<8 hex>` with
a random nonce so two tabs on the same run get independent sessions.
`SweepViews()` lists sessions (`tmux ls -F '#{session_name} #{session_attached}'`)
and kills every unattached `erbrus-view-*` session; it runs once at
`serve` startup for leftovers of a crashed server. Attached ones belong
to a live handler and are left alone.

### `internal/termview` — the pty

```go
type Term struct { /* pty file, cmd */ }
func Start(argv []string, cols, rows int, env []string) (*Term, error) // pty.StartWithSize
func (t *Term) Read(p []byte) (int, error)   // output bytes
func (t *Term) Write(p []byte) (int, error)  // input bytes
func (t *Term) Resize(cols, rows int) error  // pty.Setsize
func (t *Term) Close() error                 // kill process group, close pty, wait
```

Environment: the parent environment plus `TERM=xterm-256color` and
`COLORTERM=truecolor` so tmux renders RGB colors to the client. The file
`termview_windows.go` (build tag `windows`) makes `Start` return
`ErrUnsupported`; `build.sh`'s Windows cross-compile keeps working and the
handler answers 501 there.

### `internal/server/terminal.go` — the websocket handler

`GET /ui/runs/{id}/terminal` (registered next to the screen routes):

1. Load the run. Not found, no tmux target, or status not
   starting/running: 404 before the upgrade.
2. `s.spawner.(spawn.Viewer)` false: 501 "terminal not supported for this spawner".
3. Enforce `maxTerminals` (8) open at once: 503 above it.
4. `websocket.Accept(w, r, nil)`. The library's default origin check
   requires `Origin` to match `Host`; requests without `Origin` are
   accepted. That is the same policy as `originGuard`.
5. `cols, rows := Size(h)`; `Start(ViewCommand(h, ViewName(run.ID)), cols, rows, env)`.
   On error: close the socket with code 1011 and the error text; the page
   shows it and falls back.
6. Send a text frame `{"cols":C,"rows":R}`. Then three goroutines:
   - pty → ws: 32 KiB reads, `MessageBinary` writes.
   - ws → pty: binary or text frames written verbatim.
   - size poll every 2 s: on change, `Resize` the pty and send a new
     `{"cols","rows"}` text frame. A `Size` error (window gone) closes
     the socket with code 1000 and reason `window gone`.
7. Any pump ending closes everything: socket, `Term.Close()`, which ends
   the tmux client, which triggers `destroy-unattached`.

Handlers hold a per-run count only for the limit; nothing is shared
between connections. `Server.Close()` (or the existing shutdown path)
closes every open `Term`.

### Screen page

`screen.html` gains, for live tmux runs on a `Viewer` spawner
(`page.Terminal = true`), a `<div id="term" data-ws="/ui/runs/{{.Run.ID}}/terminal">`
above the existing `<pre id="screen">`, which starts hidden. The head
loads `vendor/xterm.css` and `vendor/xterm.js` through `asset` like the
other assets; other pages do not load them.

`app.js` terminal block:

- Creates `new Terminal({cursorBlink:true, fontFamily: <mono stack>, scrollback: 0, convertEol:false})`,
  opens it in `#term`, connects the websocket with `binaryType='arraybuffer'`.
- First text frame sets `term.resize(cols, rows)`; later ones resize
  again. No fit addon: the terminal is exactly the window's size and the
  page scrolls if the browser is smaller (the page already has
  `overflow-x:auto`).
- `term.onData` and `term.onBinary` send to the socket. Browser-owned
  shortcuts (Ctrl+W, Ctrl+T, Ctrl+N, Ctrl+Shift+*) cannot be intercepted
  and are not; Ctrl+C reaches the agent as an interrupt; Ctrl+V pastes
  through xterm's paste handling (xterm sends bracketed paste when the
  app enabled it, which Claude Code and codex do).
- Socket close before any output, or with code 1011/1006: hide `#term`,
  show `#screen`, put the close reason in `#screenerr`, and let the
  round 1 SSE code take over as today. Close with reason `window gone`:
  same fallback, the SSE feed then reports the final screen or "screen
  unavailable" as before.
- The state header and keypad keep working: the page subscribes to
  `/screen/events?html=0` and the server omits `html` from frames when
  that parameter is set, so the 500 ms capture loop no longer ships the
  full screen twice. In fallback mode the page resubscribes without the
  parameter.
- Focus: clicking the terminal focuses xterm (it owns a hidden textarea);
  the page shows a "typing here" border on focus like the composer. The
  keypad's "to type text, use the channel composer" hint is dropped when
  the terminal is present.

### Vendoring xterm.js

`scripts/vendor-xterm.sh` downloads pinned files from unpkg into
`internal/web/static/vendor/`:

```
@xterm/xterm@6.0.0/lib/xterm.js   -> vendor/xterm.js
@xterm/xterm@6.0.0/css/xterm.css  -> vendor/xterm.css
@xterm/xterm@6.0.0/LICENSE        -> vendor/xterm.LICENSE
```

and writes `vendor/VERSIONS` (`@xterm/xterm 6.0.0`). The files are
committed; the script exists so an upgrade is one command and a diff.
`web.AssetVersion` already hashes everything under `static/`.

### Dependencies

`go.mod` gains `github.com/coder/websocket` (v1.8.15, ISC, zero transitive
deps) and `github.com/creack/pty` (v1.1.24, MIT, zero deps). Both are
pure Go; `CGO_ENABLED=0` builds stay possible.

## Data flow

Open: page load → GET terminal → 101 → `Size` (1 tmux call) →
`Start` (pty + tmux client) → `{"cols","rows"}` → tmux redraws the window
into the pty → binary frames → xterm renders. First paint is one tmux
redraw, well under 100 ms locally.

Type: keypress → xterm `onData` → binary frame → pty write → tmux client
→ pane → agent → pane output → tmux renders to every client, including
the pty → binary frame → xterm. The user's terminal shows the same
keystroke through its own client.

Leave: tab closes → socket closes → handler closes pty → tmux client
exits → `destroy-unattached` removes the view session (verified 0.5 s).

Agent exits: with `remain-on-exit` the pane stays (dead), the view keeps
showing it; Reconcile marks the run failed/done and, when the window is
finally killed, `Size` fails and the socket closes with `window gone`.

## Error handling

| Situation | Behavior |
|---|---|
| Run not running / no tmux target | 404 before upgrade; page never shows `#term` (server decides `Terminal`) |
| Spawner without `Viewer` (tests' fake, future zellij) | 501; page falls back to SSE |
| Windows host | `termview.ErrUnsupported` → 501 |
| tmux session gone between page render and connect | `Size` error → 1011 with text; fallback |
| pty start fails (no /dev/ptmx, tmux missing) | 1011 with text; fallback |
| Too many terminals | 503 before upgrade; page shows "too many open terminals" in `#screenerr` and falls back |
| Browser closes mid-output | pump write error → everything closed |
| erbrus stops | all `Term`s closed on shutdown; if it crashed, `SweepViews` at next start kills unattached `erbrus-view-*` |

Nothing is posted to the channel for typed input; the human in the
terminal is not an event.

## Security

Localhost single-user UI, unchanged. The websocket is same-origin by the
library's default check. The view client runs with the user's own
environment, as every other tmux call does. `prefix None` on the view
session stops a browser tab from issuing tmux commands (kill-window,
switch, copy mode) through the terminal; erbrus's own control paths
(`Send`, `SendKeys`, `Stop`) are unaffected.

## Testing

- `internal/spawn`: `ViewCommand` argv is exactly the command above for a
  known handle and view name; malformed handle errors; `Size` parses
  `"120 35"`; `ViewName` shape; `SweepViews` kills only unattached
  `erbrus-view-*` names (fake runner records calls).
- `internal/termview` (linux): `Start(["sh","-c","stty size; cat"], 80, 24)`
  reads `24 80`; writing `hello\n` reads back an echo; `Resize(100,30)`
  followed by a `stty size` child prints `30 100`; `Close` reaps the
  process. Skips when `/dev/ptmx` is unavailable.
- `internal/server`: `fakeSpawner` gains `ViewCommand`/`Size` returning
  `["sh","-c","stty size; cat"]` and 100x30. Dial with the library's
  client: first frame is `{"cols":100,"rows":30}`, a binary frame
  contains `30 100`, `ping\r` comes back, closing the client ends the
  child. Non-running run → 404; a spawner without `Viewer` → 501; a size
  error → close code 1011. `/screen/events?html=0` frames have no `html`
  key. The screen page renders `#term` with the vendored asset URLs only
  when `Terminal` is true.
- Integration (`ERBRUS_TMUX_TEST=1`, skipped otherwise, session name
  `claudetest-term-<pid>`): real tmux window running `cat`; through the
  handler, typed bytes appear in `capture-pane`; the window size is
  unchanged with a smaller pty; the view session is gone after close.
- Manual: throwaway instance on port 7499 (XDG dirs under the scratchpad,
  `session_pattern: claudetest-{project}`), open a live agent's screen,
  type a prompt, answer a dialog with arrows, confirm the user-facing
  terminal size never changes.

## Out of scope and known limits

- **Mouse / wheel scrollback.** Copy mode is shared by every viewer of the
  pane; wheel-scrolling from the browser would freeze the agent's pane in
  the user's terminal and swallow `send-keys`. The view session keeps
  `mouse off` and no prefix. A read-only history view via
  `capture-pane -S -N` can come later without this problem.
- **Resize from the browser.** The window's size belongs to the user's
  real terminal. When no real client is attached the window keeps its
  last size (the "takeover" case); erbrus does not take it over.
- **Non-tmux spawners and Windows hosts** get the read-only page.
- **Auth.** None added; same as the rest of the UI.
- **Two terminals on one run** are two independent view sessions; both
  work, both type into the same pane.
- xterm.js is loaded only on the screen page; nothing else changes size.
