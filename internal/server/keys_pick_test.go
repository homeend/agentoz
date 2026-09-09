package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"erbrus/internal/spawn"
)

// agy's trust dialog: cursor-style choices. "Esc to cancel" is appended so
// the claude-code test provider classifies it as a question.
const agyDialog = "Do you trust the contents of this project?\n" +
	"Antigravity CLI requires permission to read, edit, and execute files here.\n" +
	"> Yes, I trust this folder\n" +
	"  No, exit\n" +
	"  ↑/↓ Navigate · enter Confirm\n" +
	"Esc to cancel\n"

func TestKeypadPicksCursorOptions(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	old := keyGap
	keyGap = 0
	defer func() { keyGap = old }()

	fs.setScreen("s:5", spawn.Screen{Raw: agyDialog, Activity: time.Now()})
	testSrv.WatchScreens()
	resp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	body := readAll(t, resp)
	for _, want := range []string{`value="pick:0"`, `› Yes, I trust this folder</button>`, `value="pick:1"`, `>No, exit</button>`, `title="select this and confirm"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("keypad missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Navigate</button>") || strings.Contains(body, `value="Down"`) {
		t.Fatalf("hint offered as a choice or arrows shown: %s", body)
	}

	// pick:1 walks one down and confirms; pick:0 (already there) just confirms.
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID), url.Values{"key": {"pick:1"}, "channel": {fmt.Sprint(ch1)}})
	r.Body.Close()
	if r.StatusCode != http.StatusFound || strings.Contains(r.Header.Get("Location"), "warning") {
		t.Fatalf("pick:1: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if got := strings.Join(fs.keys, ","); got != "s:5|Down,s:5|Enter" {
		t.Fatalf("keys = %s", got)
	}
	fs.keys = nil
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID), url.Values{"key": {"pick:0"}, "back": {"screen"}})
	r.Body.Close()
	if got := strings.Join(fs.keys, ","); got != "s:5|Enter" {
		t.Fatalf("keys = %s", got)
	}

	// Cursor moved to the second line meanwhile: pick:0 walks Up.
	fs.keys = nil
	fs.setScreen("s:5", spawn.Screen{Raw: strings.Replace(strings.Replace(agyDialog, "> Yes", "  Yes", 1), "  No, exit", "> No, exit", 1), Activity: time.Now()})
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID), url.Values{"key": {"pick:0"}})
	r.Body.Close()
	if got := strings.Join(fs.keys, ","); got != "s:5|Up,s:5|Enter" {
		t.Fatalf("keys = %s", got)
	}

	// The dialog is gone: nothing pressed, a warning instead.
	fs.keys = nil
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n" + box, Activity: time.Now()})
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID), url.Values{"key": {"pick:1"}})
	r.Body.Close()
	if !strings.Contains(r.Header.Get("Location"), "warning=") || len(fs.keys) != 0 {
		t.Fatalf("stale pick: %s keys=%v", r.Header.Get("Location"), fs.keys)
	}
	// Out-of-range and malformed picks are rejected before any capture.
	for _, k := range []string{"pick:9", "pick:x", "pick:", "pick:10"} {
		r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID), url.Values{"key": {k}})
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d", k, r.StatusCode)
		}
	}
}
