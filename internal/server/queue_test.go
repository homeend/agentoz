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

// A message composed while the agent shows a dialog (or is still
// starting) must not be pasted into the dialog; it waits for the input
// box. Seen live: a message sent 3 s after spawning Antigravity vanished
// into its trust dialog.
func TestComposerQueuesUntilInputBox(t *testing.T) {
	ts, st, root := newTestServer(t)
	fastPaste(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	fs.setScreen("s:5", spawn.Screen{Raw: "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n", Activity: time.Now()})
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"body": {"do the thing"}, "target": {fmt.Sprintf("r%d", run.ID)}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	loc := r.Header.Get("Location")
	if r.StatusCode != http.StatusFound || !strings.Contains(loc, "queued") || !strings.Contains(loc, "dialog") {
		t.Fatalf("expected a queued notice, got %d %s", r.StatusCode, loc)
	}
	// ?queued=<agent> lets the page retire the notice on the delivery note.
	if !strings.Contains(loc, "&queued="+run.AgentName) {
		t.Fatalf("redirect should name the queued agent, got %s", loc)
	}
	time.Sleep(50 * time.Millisecond)
	if got := waitSent(fs, 30*time.Millisecond); len(got) != 0 {
		t.Fatalf("pasted into the dialog: %v", got)
	}

	// The dialog is answered; the input box is back: delivered, with a note.
	fs.setScreen("s:5", spawn.Screen{Raw: "done\n" + box, Activity: time.Now()})
	sent := waitSent(fs, 2*time.Second)
	if len(sent) != 1 || !strings.Contains(sent[0], "do the thing") {
		t.Fatalf("not delivered after the dialog: %v", sent)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		msgs, _ := st.MessagesSince(ch1, 0, 100)
		var bodies []string
		for _, m := range msgs {
			bodies = append(bodies, m.Body)
		}
		if strings.Contains(strings.Join(bodies, "\n"), "queued message delivered") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no delivery note; messages: %v", bodies)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A blank pane on a fresh run is "still starting": queued as well.
	fs.setScreen("s:5", spawn.Screen{Raw: "\n\n\n", Activity: time.Now()})
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"body": {"early bird"}, "target": {fmt.Sprintf("r%d", run.ID)}})
	r.Body.Close()
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "starting") {
		t.Fatalf("blank pane on a fresh run must queue: %s", loc)
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "ready\n" + box, Activity: time.Now()})
	deadline = time.Now().Add(2 * time.Second)
	for len(waitSent(fs, 10*time.Millisecond)) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sent := waitSent(fs, 10*time.Millisecond); len(sent) != 2 || !strings.Contains(sent[1], "early bird") {
		t.Fatalf("queued early message not delivered: %v", sent)
	}

	// A busy agent past its boot takes text at once (it queues typed input
	// itself); within the boot grace "working" may be the sign-in spinner.
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n" + box, Activity: time.Now()})
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"body": {"too early"}, "target": {fmt.Sprintf("r%d", run.ID)}})
	r.Body.Close()
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "starting") {
		t.Fatalf("working during boot must queue: %s", loc)
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "ok\n" + box, Activity: time.Now()})
	deadline = time.Now().Add(2 * time.Second)
	for len(waitSent(fs, 10*time.Millisecond)) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sent := waitSent(fs, 10*time.Millisecond); len(sent) != 3 || !strings.Contains(sent[2], "too early") {
		t.Fatalf("queued boot-time message not delivered: %v", sent)
	}
	oldGrace := bootGrace
	bootGrace = 0 // the run is "old" now
	defer func() { bootGrace = oldGrace }()
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n" + box, Activity: time.Now()})
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"body": {"and this"}, "target": {fmt.Sprintf("r%d", run.ID)}})
	r.Body.Close()
	if loc := r.Header.Get("Location"); strings.Contains(loc, "warning") {
		t.Fatalf("busy agent should get the text directly: %s", loc)
	}
	if sent := waitSent(fs, 500*time.Millisecond); len(sent) != 4 || !strings.Contains(sent[3], "and this") {
		t.Fatalf("sent = %v", sent)
	}
}
