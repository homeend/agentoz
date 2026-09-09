package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"erbrus/internal/store"
)

func TestRemoveAllStoppedRuns(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	mk := func(status string) store.AgentRun {
		r, err := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex", AgentName: "a", Workdir: "/w", Status: status, Spawner: "tmux"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	mk("done")
	mk("stopped")
	live := mk("running")

	resp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	body := readAll(t, resp)
	form := fmt.Sprintf(`action="/ui/channels/%d/runs/delete-finished"`, ch1)
	if !strings.Contains(body, form) || !strings.Contains(body, "Remove all stopped (2)") || !strings.Contains(body, `data-confirm="Remove all 2 stopped run(s)`) {
		t.Fatalf("button missing or wrong count:\n%s", body)
	}

	// The live-refreshed panel carries the button too, so it shows up the
	// moment a run stops while the page is open.
	resp, _ = http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if body := readAll(t, resp); !strings.Contains(body, form) {
		t.Fatalf("runs panel without the button:\n%s", body)
	}

	r, err := noRedirect().Post(fmt.Sprintf("%s/ui/channels/%d/runs/delete-finished", ts.URL, ch1), "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "removed+2+stopped") {
		t.Fatalf("redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	runs, _ := st.RunsByChannel(ch1)
	if len(runs) != 1 || runs[0].ID != live.ID {
		t.Fatalf("runs left = %+v", runs)
	}
	// Nothing stopped: the button is gone.
	resp, _ = http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if body := readAll(t, resp); strings.Contains(body, form) {
		t.Fatal("button shown with no stopped runs")
	}
	// Unknown channel.
	r, _ = noRedirect().Post(ts.URL+"/ui/channels/999999/runs/delete-finished", "application/x-www-form-urlencoded", nil)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown channel: %d", r.StatusCode)
	}
}
