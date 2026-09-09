package server

import (
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// checkedSender is what the tmux spawner offers beyond Spawner.Send: a
// delivery that asks the caller whether the agent took the message.
type checkedSender interface {
	SendChecked(h spawn.Handle, text string, submitted func(screen string) bool) error
}

// deliver types text into a run's terminal. With a spawner that supports
// it, the provider's own screen rules decide "submitted": a screen that
// classifies as working or as a question means the agent took the text,
// whatever its input box still shows.
func (s *Server) deliver(run store.AgentRun, text string) error {
	h := spawn.Handle(run.TmuxTarget)
	cs, ok := s.spawner.(checkedSender)
	if !ok {
		return s.spawner.Send(h, text)
	}
	return cs.SendChecked(h, text, s.submittedBy(run.Provider))
}

// submittedBy reports, for one provider's rules, whether a screen shows
// an agent that has accepted its input (busy, or asking something).
func (s *Server) submittedBy(provider string) func(string) bool {
	rules := s.rulesFor(provider)
	return func(raw string) bool {
		st := screen.Classify(rules, screen.Tail(screen.Strip(raw), 15))
		return st == screen.Working || st == screen.Question
	}
}
