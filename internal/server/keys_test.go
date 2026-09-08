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

func TestKeypadShownOnQuestionAndSendsKeys(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	// No keypad while working.
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n" + box, Activity: time.Now()})
	testSrv.WatchScreens()
	resp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if body := readAll(t, resp); strings.Contains(body, `class="keypad"`) {
		t.Fatalf("keypad shown while working: %s", body)
	}
	// Keypad appears on a question.
	fs.setScreen("s:5", spawn.Screen{Raw: "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n", Activity: time.Now()})
	testSrv.WatchScreens()
	resp, _ = http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	body := readAll(t, resp)
	for _, want := range []string{`class="keypad"`, fmt.Sprintf(`action="/ui/runs/%d/keys"`, run.ID), `value="1"`, `value="Escape"`, `value="Enter"`, `value="Down"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("keypad missing %q: %s", want, body)
		}
	}
	// The screen page always offers it.
	sp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if sb := readAll(t, sp); !strings.Contains(sb, `class="keypad"`) {
		t.Fatalf("screen page lacks keypad: %s", sb)
	}

	// Pressing "1" sends exactly that key and redirects back to the channel.
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID),
		url.Values{"key": {"1"}, "channel": {fmt.Sprint(ch1)}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || r.Header.Get("Location") != fmt.Sprintf("/ui/channels/%d", ch1) {
		t.Fatalf("keys redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if len(fs.keys) != 1 || fs.keys[0] != "s:5|1" {
		t.Fatalf("keys sent = %v", fs.keys)
	}
	// Back to the screen page when asked.
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID),
		url.Values{"key": {"Escape"}, "back": {"screen"}})
	r.Body.Close()
	if loc := r.Header.Get("Location"); loc != fmt.Sprintf("/ui/runs/%d/screen", run.ID) {
		t.Fatalf("screen redirect: %s", loc)
	}
	if fs.keys[1] != "s:5|Escape" {
		t.Fatalf("keys sent = %v", fs.keys)
	}
	// Only the allow-listed keys go through.
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/runs/%d/keys", ts.URL, run.ID), url.Values{"key": {"C-c"}})
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest || len(fs.keys) != 2 {
		t.Fatalf("disallowed key: status %d keys %v", r.StatusCode, fs.keys)
	}
}

func TestDeadPaneNeverStalls(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	old := time.Now().Add(-stallAfter - time.Minute)
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (9m 0s · x)\n" + box, Activity: old, Dead: true})
	testSrv.WatchScreens()
	if rs, _ := testSrv.stateOf(run.ID); rs.Stalled {
		t.Fatalf("dead pane reported stalled: %+v", rs)
	}
}
