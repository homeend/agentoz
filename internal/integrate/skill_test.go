package integrate

import (
	"strings"
	"testing"

	"erbrus/internal/agentskill"
)

// The skill's "working as an erbrus agent" part and the spawn preamble
// teach the same protocol; keep the load-bearing phrases in both.
func TestPreambleAndSkillAgree(t *testing.T) {
	p := Preamble("/abs/erbrus", "a", "general")
	s := agentskill.Body()
	for _, want := range []string{"msg send --report --md", "<<'EOF'", "--file", "Do not start any work on your own"} {
		if !strings.Contains(p, want) || !strings.Contains(s, want) {
			t.Errorf("%q must appear in both preamble and skill", want)
		}
	}
}
