package integrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreamble(t *testing.T) {
	p := Preamble("/abs/bin/erbrus", "review-kimi", "wt/feat")
	for _, want := range []string{
		"review-kimi",
		"wt/feat",
		"/abs/bin/erbrus msg send --report",
		"before the message text",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("preamble missing %q\n%s", want, p)
		}
	}
	if strings.Contains(p, "msg read") {
		t.Error("preamble must not tell agents to read channel history (removed by request)")
	}
}

func TestAssemblePrompt(t *testing.T) {
	got := AssemblePrompt("PRE", "TASK", "CTX")
	if got != "PRE\n\nTASK\n\nCTX" {
		t.Errorf("got %q", got)
	}
	if got := AssemblePrompt("PRE", "TASK", ""); got != "PRE\n\nTASK" {
		t.Errorf("empty context: %q", got)
	}
}

func TestHandoffContext(t *testing.T) {
	got := HandoffContext("webshop", "wt/feat", "impl-codex", "2026-08-27 10:00:00",
		"6 files changed", []string{"/data/artifacts/12/analysis.md"})
	for _, want := range []string{"webshop", "wt/feat", "impl-codex", "6 files changed", "/data/artifacts/12/analysis.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("context missing %q\n%s", want, got)
		}
	}
}

func TestProviderHookClaude(t *testing.T) {
	dir := t.TempDir()
	args, err := ProviderHook("claude-code", dir, "/abs/erbrus")
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "claude-settings.json")
	if args != "--settings "+settings {
		t.Errorf("args = %q", args)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("settings not valid JSON: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "'/abs/erbrus' msg send --system") {
		t.Errorf("stop hook must invoke the quoted absolute binary:\n%s", data)
	}
	if !strings.Contains(string(data), "Stop") {
		t.Errorf("no Stop hook in settings:\n%s", data)
	}
}

func TestProviderHookUnknownProvider(t *testing.T) {
	args, err := ProviderHook("junie", t.TempDir(), "/abs/erbrus")
	if err != nil || args != "" {
		t.Errorf("unknown provider must be a no-op, got %q, %v", args, err)
	}
}

func TestProviderHookCodex(t *testing.T) {
	dir := t.TempDir()
	args, err := ProviderHook("codex", dir, "/abs/erbrus")
	if err != nil {
		t.Fatal(err)
	}
	notifyScript := filepath.Join(dir, "notify.sh")
	if !strings.Contains(args, notifyScript) {
		t.Errorf("args must reference the notify script, got %q", args)
	}
	if !strings.Contains(args, "-c notify=") {
		t.Errorf("args must contain -c notify=, got %q", args)
	}
	data, err := os.ReadFile(notifyScript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "'/abs/erbrus' msg send --system") {
		t.Errorf("notify script must invoke the quoted absolute binary:\n%s", data)
	}
	// Check executable permission
	info, err := os.Stat(notifyScript)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("notify script must be executable, mode = %o", info.Mode())
	}
}

func TestForwardToAgent(t *testing.T) {
	got := ForwardToAgent("/abs/erbrus", 7, "webshop", "general", "main", "/code/webshop",
		"claude", "2026-08-27 10:00:00", "auth is done", []string{"/data/artifacts/3/report.md"})
	for _, want := range []string{
		"forwarded from webshop / #general",
		"branch main",
		"worktree /code/webshop",
		"by claude at 2026-08-27 10:00:00",
		"auth is done",
		"attached file (read it yourself): /data/artifacts/3/report.md",
		`/abs/erbrus msg send --report --channel 7 "your reply"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestChatReplySuffix(t *testing.T) {
	got := ChatReplySuffix("/abs/erbrus")
	if !strings.Contains(got, `/abs/erbrus msg send --report`) {
		t.Fatalf("missing reply command: %s", got)
	}
	if !strings.HasPrefix(got, "\n\n(") {
		t.Fatalf("suffix must be separated from the message body: %q", got[:8])
	}
}
