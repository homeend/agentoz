package spawn

import (
	"strings"
	"testing"
)

// The text sits under a rule (the heuristic says "pending") but the
// caller's check says the agent took it: one Enter, no error.
func TestSendCheckedTrustsTheCallersVerdict(t *testing.T) {
	sendSettle, sendVerifyDelay = 0, 0
	pending := "──────────\n> hello there agent\n──────────\nesc to cancel\n"
	rec := &recorder{out: map[string]string{"tmux capture-pane": pending}}
	asked := 0
	err := NewTmux(rec.run).SendChecked("s:1", "hello there agent", func(screen string) bool {
		asked++
		return strings.Contains(screen, "esc to cancel")
	})
	if err != nil {
		t.Fatal(err)
	}
	enters := 0
	for _, c := range rec.calls {
		if strings.HasSuffix(c, " Enter") {
			enters++
		}
	}
	if enters != 1 || asked != 1 {
		t.Fatalf("enters=%d asked=%d calls=%v", enters, asked, rec.calls)
	}
	// A false verdict falls through to the heuristic: still pending → retries → error.
	rec = &recorder{out: map[string]string{"tmux capture-pane": pending}}
	err = NewTmux(rec.run).SendChecked("s:1", "hello there agent", func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "did not submit") {
		t.Fatalf("expected the heuristic's error, got %v", err)
	}
}
