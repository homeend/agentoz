package agentskill

import (
	"fmt"
	"strings"
	"testing"
)

func TestSkillFileShape(t *testing.T) {
	f := SkillFile()
	for _, want := range []string{
		"---\nname: erbrus\n", "description: Use when", "---\n\n",
		fmt.Sprintf("<!-- erbrus:erbrus:v%d -->", Version),
		"erbrus msg send --report --md", "<<'EOF'", "erbrus provider set", "erbrus screen capture --run", "erbrus screen test --provider",
		"screen_working", "screen_waiting", "screen_question", "prompt: paste", "{prompt}", "{model}", "ERBRUS_RUN_ID",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("skill missing %q", want)
		}
	}
	if !strings.HasPrefix(f, "---\n") {
		t.Error("frontmatter must open the file")
	}
	if strings.Count(f, "\n---\n") != 1 {
		t.Errorf("exactly one frontmatter close expected:\n%s", f[:200])
	}
}

func TestMarkerParsing(t *testing.T) {
	if !HasMarker([]byte(SkillFile())) || InstalledVersion([]byte(SkillFile())) != Version {
		t.Fatal("rendered file must carry the current marker")
	}
	old := []byte("---\nname: erbrus\n---\n<!-- erbrus:erbrus:v0 -->\nold")
	if !HasMarker(old) || InstalledVersion(old) != 0 {
		t.Errorf("v0 marker: has=%v v=%d", HasMarker(old), InstalledVersion(old))
	}
	if HasMarker([]byte("---\nname: erbrus\n---\nsomebody else's file")) {
		t.Error("foreign file must not count as ours")
	}
	if InstalledVersion(nil) != -1 {
		t.Error("no marker must read as -1")
	}
}
