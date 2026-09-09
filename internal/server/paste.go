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
// has nowhere to put it (Kimi Code's interactive mode takes none).
func pasteMode(p config.Provider) bool {
	switch p.Prompt {
	case "paste":
		return true
	case "arg":
		return false
	}
	return !strings.Contains(p.Command, "{prompt}")
}

// delivery describes one text waiting for an agent's input box.
type delivery struct {
	what string // "prompt" | "message" — for the channel notes
	// acceptWorking: a busy agent is fine too (chat: Claude Code and
	// Antigravity queue typed input while they work). The spawn prompt
	// waits for the input box proper.
	acceptWorking bool
}

var (
	promptDelivery = delivery{what: "prompt"}
	chatDelivery   = delivery{what: "message", acceptWorking: true}
)

// deliverPrompt waits for the agent's input box and pastes prompt once.
// Runs in its own goroutine right after a tmux spawn.
func (s *Server) deliverPrompt(run store.AgentRun, prompt string) {
	s.deliverWhenReady(run, prompt, promptDelivery)
}

// readyFor: can text be typed into a screen in state st right now?
func (d delivery) readyFor(st screen.State) bool {
	return st == screen.Waiting || (d.acceptWorking && st == screen.Working)
}

// bootGrace: a run younger than this whose screen is not yet classified
// is still starting (banner, trust check); text typed now can be lost.
var bootGrace = 30 * time.Second

// mustQueue says why a chat message cannot be typed into run right now
// ("" when it can): a dialog is up, or the agent is still starting. A
// screen the rules cannot read on a run past its boot is typed into
// directly, as before — queuing every message to a rule-less provider
// would only add noise.
func (s *Server) mustQueue(run store.AgentRun) string {
	sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
	if err != nil {
		return ""
	}
	young := time.Since(run.CreatedAt) < bootGrace
	if strings.TrimSpace(screen.Strip(sc.Raw)) == "" {
		// A blank pane on a fresh run: the CLI has not drawn yet; text
		// typed now lands in whatever it shows first (its trust dialog).
		if young {
			return "is still starting"
		}
		return ""
	}
	st := screen.Classify(s.rulesFor(run.Provider), screen.Tail(screen.Strip(sc.Raw), 15))
	switch {
	case chatDelivery.readyFor(st):
		return ""
	case st == screen.Question:
		return "is showing a dialog (answer it on its card)"
	case st == screen.Unknown && young:
		return "is still starting"
	}
	return ""
}

// deliverWhenReady polls the screen until the agent can take text, then
// delivers once and notes the outcome in the channel. A question on
// screen (a trust dialog, a permission prompt) is the human's to answer;
// pasting into it would answer the dialog with the text's Enter and lose
// the text (seen live 2026-09-09: a message sent 3 s after spawning
// Antigravity vanished into its trust dialog). Delivery waits instead.
func (s *Server) deliverWhenReady(run store.AgentRun, text string, d delivery) {
	// Pacing is read once: tests shorten the package vars per test.
	poll, wait, settle, hardMax := pastePoll, pasteDeadline, pasteSettle, pasteMax
	h := spawn.Handle(run.TmuxTarget)
	rules := s.rulesFor(run.Provider)
	// Only rules written for this provider (config or built-in) can be
	// trusted to say "waiting"; the generic fallback matches a bare prompt
	// glyph and nothing else, so for an unknown CLI a screen that stopped
	// changing is the best signal there is.
	p, _ := s.providerCfg(run.Provider)
	specific := screen.HasDefaults(run.Provider) || len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) > 0
	canSeeWaiting := specific && len(rules.Waiting) > 0
	start := time.Now()
	deadline := start.Add(wait)
	var lastRaw string
	var stableSince time.Time
	fail := func(reason string) {
		if d.what == "prompt" {
			s.system(run.ChannelID, fmt.Sprintf("%s: prompt NOT delivered (%s) — paste it yourself", run.AgentName, reason))
			return
		}
		s.system(run.ChannelID, fmt.Sprintf("%s: queued message NOT delivered (%s) — resend it when the agent is idle", run.AgentName, reason))
	}
	for {
		time.Sleep(poll)
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
		ready := d.readyFor(st)
		switch {
		case st == screen.Question:
			deadline = now.Add(wait)
		case !canSeeWaiting && st == screen.Unknown && len(lines) > 0:
			if sc.Raw != lastRaw {
				lastRaw, stableSince = sc.Raw, now
			} else if now.Sub(stableSince) >= settle {
				ready = true
			}
		}
		if ready {
			if err := s.deliver(run, text); err != nil {
				fail(err.Error())
				return
			}
			if d.what == "prompt" {
				s.system(run.ChannelID, fmt.Sprintf("%s: prompt delivered", run.AgentName))
			} else {
				s.system(run.ChannelID, fmt.Sprintf("%s: queued message delivered", run.AgentName))
			}
			return
		}
		if now.After(deadline) || now.Sub(start) > hardMax {
			label := string(st)
			if label == "" {
				label = "unknown"
			}
			fail("no input box within " + shortDur(now.Sub(start)) + ", screen state " + label)
			return
		}
	}
}
