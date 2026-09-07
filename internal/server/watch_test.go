package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

func TestRulesForHonorsConfigOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers = map[string]config.Provider{
		"kimi":   {Command: "kimi", ScreenQuestion: []string{"CONFIRM"}},
		"broken": {Command: "x", ScreenWorking: []string{"("}}, // falls back to generic
	}
	s := New(nil, cfg, nil)
	if got := screen.Classify(s.rulesFor("kimi"), []string{"please CONFIRM"}); got != screen.Question {
		t.Errorf("kimi override: %q", got)
	}
	if got := screen.Classify(s.rulesFor("kimi"), []string{"❯"}); got != screen.Unknown {
		t.Errorf("override must replace the whole set: %q", got)
	}
	if got := screen.Classify(s.rulesFor("broken"), []string{"(y/n)"}); got != screen.Question {
		t.Errorf("broken falls back to generic: %q", got)
	}
	if got := screen.Classify(s.rulesFor("claude-code"), []string{"❯"}); got != screen.Waiting {
		t.Errorf("unconfigured provider uses built-ins: %q", got)
	}
}

// systemBodies returns the bodies of system messages in chID, oldest first.
func systemBodies(t *testing.T, st *store.Store, chID int64) []string {
	t.Helper()
	msgs, err := st.MessagesLatest(chID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		if m.Kind == "system" {
			out = append(out, m.Body)
		}
	}
	return out
}

func countRunEvents(ev <-chan sseEvent) int {
	n := 0
	for {
		select {
		case e := <-ev:
			if e.Event == "run" {
				n++
			}
		default:
			return n
		}
	}
}

func claudeRun(t *testing.T, st *store.Store, chID int64) store.AgentRun {
	t.Helper()
	run, err := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "claude-code", AgentName: "claude",
		Workdir: "/w", Status: "running", Spawner: "tmux", TmuxTarget: "s:5"})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestWatchScreensTurnEnd(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{alive: map[spawn.Handle]bool{"s:5": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	ev, cancel := testSrv.hub.Subscribe()
	defer cancel()
	before := len(systemBodies(t, st, ch1))

	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n❯\n", Activity: time.Now()})
	if err := testSrv.WatchScreens(); err != nil {
		t.Fatal(err)
	}
	if rs, ok := testSrv.stateOf(run.ID); !ok || rs.State != screen.Working || rs.StepFor != 27*time.Second {
		t.Fatalf("after working screen: %+v", rs)
	}
	if n := countRunEvents(ev); n != 1 {
		t.Fatalf("run events after first classify = %d", n)
	}
	if got := systemBodies(t, st, ch1); len(got) != before {
		t.Fatalf("entering working must not post: %v", got)
	}

	fs.setScreen("s:5", spawn.Screen{Raw: "done.\n❯\n", Activity: time.Now()})
	testSrv.WatchScreens()
	got := systemBodies(t, st, ch1)
	if len(got) != before+1 || !strings.Contains(got[len(got)-1], "finished its turn") {
		t.Fatalf("turn end message: %v", got)
	}
	if n := countRunEvents(ev); n != 1 {
		t.Fatalf("run events on turn end = %d", n)
	}

	testSrv.WatchScreens() // same screen: nothing
	fs.setScreen("s:5", spawn.Screen{Raw: "", Activity: time.Now()})
	testSrv.WatchScreens() // unknown: nothing
	if got := systemBodies(t, st, ch1); len(got) != before+1 {
		t.Fatalf("repeat/unknown posted: %v", got)
	}
	if n := countRunEvents(ev); n != 0 {
		t.Fatalf("run events on repeat/unknown = %d", n)
	}
	if rs, _ := testSrv.stateOf(run.ID); rs.State != screen.Waiting {
		t.Fatalf("unknown must keep state: %+v", rs)
	}

	// Rail badge.
	resp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if body := readAll(t, resp); !strings.Contains(body, `class="state st-waiting"`) || !strings.Contains(body, ">waiting ") {
		t.Fatalf("badge missing: %s", body)
	}

	// Finished runs drop their state.
	st.FinishRun(run.ID, "done", 0)
	testSrv.WatchScreens()
	if _, ok := testSrv.stateOf(run.ID); ok {
		t.Fatal("state kept for finished run")
	}
}

func TestWatchScreensQuestionAndStall(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	before := len(systemBodies(t, st, ch1))

	fs.setScreen("s:5", spawn.Screen{Raw: "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n", Activity: time.Now()})
	testSrv.WatchScreens()
	got := systemBodies(t, st, ch1)
	if len(got) != before+1 || !strings.Contains(got[len(got)-1], "needs your input") {
		t.Fatalf("question message: %v", got)
	}

	// Working but silent for longer than stallAfter → stalled once.
	old := time.Now().Add(-stallAfter - time.Minute)
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (9m 0s · x)\n❯\n", Activity: old})
	testSrv.WatchScreens()
	testSrv.WatchScreens()
	got = systemBodies(t, st, ch1)
	if len(got) != before+2 || !strings.Contains(got[len(got)-1], "stalled") {
		t.Fatalf("stall message: %v", got)
	}
	if rs, _ := testSrv.stateOf(run.ID); !rs.Stalled {
		t.Fatalf("stalled flag: %+v", rs)
	}
	resp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if body := readAll(t, resp); !strings.Contains(body, "stalled · working 9m") {
		t.Fatalf("stalled badge: %s", body)
	}
	// Output resumes → flag clears, no message.
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (9m 5s · x)\n❯\n", Activity: time.Now()})
	testSrv.WatchScreens()
	if rs, _ := testSrv.stateOf(run.ID); rs.Stalled {
		t.Fatalf("stalled not cleared: %+v", rs)
	}
	if got := systemBodies(t, st, ch1); len(got) != before+2 {
		t.Fatalf("clearing stall posted: %v", got)
	}
	// Silence while waiting is not a stall.
	fs.setScreen("s:5", spawn.Screen{Raw: "❯\n", Activity: old})
	testSrv.WatchScreens()
	if rs, _ := testSrv.stateOf(run.ID); rs.Stalled {
		t.Fatalf("waiting must not stall: %+v", rs)
	}
}
