package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"erbrus/internal/spawn"
)

// A run whose window is gone must not show its last known state: the
// header says "not available" instead of "idle" (seen live 2026-09-08
// after killing an agent's window with the screen page open).
func TestScreenPageUnavailableState(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{captureErr: fmt.Errorf("can't find window: 3")}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	body := readAll(t, resp)
	if !strings.Contains(body, `class="state st-unavailable" data-state="unavailable"`) || !strings.Contains(body, ">not available<") {
		t.Fatalf("unavailable badge missing:\n%s", body)
	}
	if !strings.Contains(body, "screen unavailable: can&#39;t find window: 3") { // html/template escapes the quote
		t.Fatalf("error text missing:\n%s", body)
	}

	// Window back: the badge is the classified state again.
	fs.captureErr = nil
	fs.setScreen("s:5", spawn.Screen{Raw: box})
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if body := readAll(t, resp); !strings.Contains(body, `data-state="waiting"`) || strings.Contains(body, "not available") {
		t.Fatalf("state not restored:\n%s", body)
	}
}
