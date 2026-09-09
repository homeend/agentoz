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

// deliverPrompt waits for the agent's input box and pastes prompt once.
// Runs in its own goroutine right after a tmux spawn. A question on
// screen (a trust dialog) is the human's to answer; delivery waits.
func (s *Server) deliverPrompt(run store.AgentRun, prompt string) {
	// Pacing is read once: tests shorten the package vars per test.
	poll, wait, settle, hardMax := pastePoll, pasteDeadline, pasteSettle, pasteMax
	h := spawn.Handle(run.TmuxTarget)
	rules := s.rulesFor(run.Provider)
	// Only rules written for this provider (config or built-in) can be
	// trusted to say "waiting"; the generic fallback matches a bare prompt
	// glyph and nothing else, so for an unknown CLI a screen that stopped
	// changing is the best signal there is.
	s.rulesMu.RLock()
	p := s.cfg.Providers[run.Provider]
	s.rulesMu.RUnlock()
	specific := screen.HasDefaults(run.Provider) || len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) > 0
	canSeeWaiting := specific && len(rules.Waiting) > 0
	start := time.Now()
	deadline := start.Add(wait)
	var lastRaw string
	var stableSince time.Time
	fail := func(reason string) {
		s.system(run.ChannelID, fmt.Sprintf("%s: prompt NOT delivered (%s) — paste it yourself", run.AgentName, reason))
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
		ready := false
		switch {
		case st == screen.Question:
			deadline = now.Add(wait)
		case st == screen.Waiting:
			ready = true
		case !canSeeWaiting && st == screen.Unknown && len(lines) > 0:
			if sc.Raw != lastRaw {
				lastRaw, stableSince = sc.Raw, now
			} else if now.Sub(stableSince) >= settle {
				ready = true
			}
		}
		if ready {
			if err := s.deliver(run, prompt); err != nil {
				fail(err.Error())
				return
			}
			s.system(run.ChannelID, fmt.Sprintf("%s: prompt delivered", run.AgentName))
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
