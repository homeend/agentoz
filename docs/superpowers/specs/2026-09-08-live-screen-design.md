# erbrus — live screen view (read-only) design

Date: 2026-09-08. Status: approved in chat (round 1: read-only viewer to test the idea).
Parent spec: /mnt/t/others/erbrus/docs/superpowers/specs/2026-08-26-erbrus-design.md

## Purpose

Show, in the web UI, what is happening inside an agent's tmux window: the
rendered screen, live, with colors. This is the foundation for two later
detections the user asked for ("nothing happens for too long" and "user
input is required"). Round 1 ships the viewer plus the cheapest idle signal
tmux gives for free. Round 2 (not in this spec) adds a per-provider
classifier over the bottom lines of the screen.

## Why capture-pane, not a byte stream or a browser terminal

- Agents are TUIs (Claude Code, codex) that redraw the whole screen. tmux
  already is the terminal emulator; `tmux capture-pane -p -e` returns the
  rendered screen with SGR color escapes. Verified on tmux 3.7c against a
  live Claude Code window.
- `tmux pipe-pane` raw bytes are unreadable for a TUI. Only useful for logs.
- xterm.js + websocket + `tmux attach` (ttyd / gotty style) gives typing and
  perfect fidelity but adds a websocket library, pty handling, and a vendored
  JS terminal, and still gives no idle / input-needed detection. Parked as a
  possible round 3. `Spawner.Send` already covers "type an answer".
- tmux format variables `#{window_activity}`, `#{pane_dead}`,
  `#{pane_width}`, `#{pane_height}` resolve on 3.7c and are read in the same
  capture.

## Scope (round 1)

In:

- A **Screen** link on every run card that has a tmux target.
- A page `/ui/runs/{id}/screen` showing the rendered pane, auto-updating,
  colors preserved, with a header: agent, provider, tmux target, run status,
  and "last activity Ns ago". The header turns amber past an idle threshold.
- Finished runs keep showing the final screen while tmux still has the
  window (remain-on-exit is already set on windows erbrus creates).
- Frames are pushed only to browsers viewing that run, never through the
  global hub.

Out (deferred):

- Typing into the terminal from the browser.
- Screen-content classification (working / awaiting input / permission
  prompt). Round 2.
- Notifications into the channel on idle. Round 2, once the classifier
  makes them trustworthy.
- Scrollback history (`capture-pane -S`). Only the visible screen.
- Persisting frames. Nothing new in the DB.

## Architecture

```
browser ── GET /ui/runs/{id}/screen ─────────► screen.html (initial frame)
browser ── GET /ui/runs/{id}/screen/events ──► per-run SSE: frame / ping
                                                    │
                                        screenFeed (server)
                                        one poll goroutine per viewed run
                                                    │ every 500 ms
                                        spawner.Capture(handle)
                                                    │
                                        tmux capture-pane -p -e
                                        tmux display -p '#{pane_dead} ...'
```

### spawn package

```go
type Screen struct {
    Raw      string    // capture-pane -p -e output, one line per row
    Dead     bool      // #{pane_dead} == 1
    Activity time.Time // #{window_activity}, zero if unparsable
    Cols     int       // #{pane_width}
    Rows     int       // #{pane_height}
}

// Spawner gains:
Capture(h Handle) (Screen, error)
```

Tmux implementation: two commands, both through `exact()` (the 2026-08-28
prefix-matching bug applies to every target).

```
tmux capture-pane -p -e -t =<h>
tmux display -p -t =<h> #{pane_dead} #{window_activity} #{pane_width} #{pane_height}
```

An error from either command (window gone) is returned as an error; the
feed reports it to the viewer as "screen unavailable".

The test fake in `internal/server/runs_test.go` gets a `Capture` returning a
scripted `Screen` per handle, plus an optional error.

### server: ANSI to HTML (`internal/server/ansi.go`)

`ansiToHTML(raw string) template.HTML`. Whitelist converter, pure function:

- Recognized: CSI `m` (SGR) with 0, 1, 2, 3, 4, 7, 22, 23, 24, 27, 30–37,
  39, 40–47, 49, 90–97, 100–107, 38;5;n, 48;5;n, 38;2;r;g;b, 48;2;r;g;b.
- Every other CSI, OSC and lone ESC sequence is dropped. All text is
  HTML-escaped. No raw HTML ever passes through.
- Output: one `<span style="...">` per styled run, text otherwise bare,
  newlines preserved (rendered inside `<pre class="screen">`). 16-color and
  256-color indices map through a fixed palette to hex. Bold, dim, italic,
  underline, reverse map to inline CSS.

### server: screen feed (`internal/server/screen.go`)

```go
type screenFrame struct {
    HTML     template.HTML `json:"html"`
    Activity int64         `json:"activity"` // unix seconds, 0 if unknown
    Dead     bool          `json:"dead"`
    Cols     int           `json:"cols"`
    Rows     int           `json:"rows"`
    Error    string        `json:"error,omitempty"` // "screen unavailable: ..."
}
```

`screenFeed` keeps, per run id, a subscriber set and the last frame's
fingerprint (sha256 over Raw, Dead, Activity). The first subscriber starts a
goroutine polling `Capture` every `screenPollInterval` (500 ms, package var
for tests); the last unsubscribe stops it. A frame is sent to subscribers
only when the fingerprint changes; a new subscriber gets the current frame
immediately. Errors from Capture become a frame with `Error` set (sent once
per change, not every tick).

Handlers:

- `GET /ui/runs/{id}/screen`: 404 if the run does not exist; renders
  `screen.html` with run metadata and an initial frame from one direct
  `Capture` (or the unavailable message). fg runs and runs without a tmux
  target render the page with "no tmux window for this run".
- `GET /ui/runs/{id}/screen/events`: SSE. Subscribes to the feed, writes
  `event: frame` with the JSON frame on each update, `event: ping` every
  `pingInterval` (reuse the existing 20 s), returns when the request context
  ends. Runs without a tmux target return 404.

Frame traffic never touches `Hub`; the existing `/events` stream and its
16-deep buffers are unaffected.

### web

- `_runs.html`: `<a class="btn small" href="/ui/runs/{{.ID}}/screen">Screen</a>`
  on cards where `TmuxTarget` is set (running or finished).
- `screen.html`: topbar with breadcrumb back to the channel; header row
  (agent · provider · status · target · activity); `<pre id="screen"
  data-run="{{.Run.ID}}">` with the initial frame; a "screen unavailable"
  line when applicable.
- `app.js`: a second self-contained block guarded by `#screen`. Opens
  `EventSource('/ui/runs/<id>/screen/events')`, swaps `innerHTML` on
  `frame`, updates the activity label every second from the last frame's
  `activity` (client computes the age), toggles an `idle` class past
  `idleAfter` (60 s), and reuses the same watchdog pattern as the channel
  page (reconnect when no ping for 45 s).
- `app.css`: `.screen` monospace block, dark background, `overflow: auto`,
  `white-space: pre`; `.screenhead`; `.idle` amber tint.

## Error handling

- Window gone / tmux error: frame with `Error`; page shows it in the header,
  keeps the last good screen if any.
- Run row missing: 404.
- Spawner nil (server without SetRuntime): page renders "no spawner", events
  endpoint 503, matching the spawn endpoints' behavior.
- Slow subscriber: per-subscriber channel of 4 frames, drop on full (the
  next change re-sends the full screen anyway; frames are idempotent).

## Testing

- `ansi_test.go`: plain text escaped; `<script>` escaped; bold/reset; 16-,
  256-, and truecolor fg/bg; unknown CSI and OSC dropped; fixture taken from
  a real Claude Code capture renders without panic and contains the visible
  text.
- `tmux_test.go`: `Capture` issues exactly the two commands with `=` targets
  and parses the display line; error on window gone.
- `screen_test.go`: feed sends a frame on subscribe, sends again only when
  the fake's screen changes, stops polling after the last unsubscribe
  (assert via the fake's call count); page handler 200 with the escaped
  screen text; events handler streams `event: frame`; 404 for an unknown
  run; fg run renders the no-window message.
- Existing suites pass unchanged apart from the fake gaining `Capture`.
- Manual: build, open a running agent's Screen page, watch it follow the
  tmux window; stop the run and confirm the final screen stays.

## Development workflow (standing rules)

Direct on main, TDD, commit after each task, rebuild `bin/erbrus`, hand over
the absolute path. No push.
