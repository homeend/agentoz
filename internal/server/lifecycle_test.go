package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

func TestProjectCardAgentBadge(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	runningAgent(t, st, ch1, "claude")
	runningAgent(t, st, ch2, "codex")

	resp, err := http.Get(ts.URL + "/ui/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, "2 agents running") {
		t.Fatalf("project card missing agent badge:\n%s", html)
	}
	if !strings.Contains(html, "agentdot") {
		t.Fatal("channel rows missing presence dot")
	}
}

func TestSidebarAgentDot(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	runningAgent(t, st, ch2, "codex")

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, "agentdot") {
		t.Fatal("sidebar missing presence dot for channel with running agent")
	}
}

func TestRunsPanelRecentCapAndExitInfo(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	// 12 finished runs; only the 10 newest may render.
	for i := 0; i < 12; i++ {
		r, err := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex",
			AgentName: fmt.Sprintf("old-%d", i), Workdir: "/w", Status: "starting", Spawner: "tmux"})
		if err != nil {
			t.Fatal(err)
		}
		status := "done"
		if i == 11 {
			status = "failed"
		}
		if err := st.FinishRun(r.ID, status, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	runningAgent(t, st, ch1, "live")

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if strings.Contains(html, "old-0") || strings.Contains(html, "old-1<") {
		t.Fatal("runs panel shows finished runs beyond the recent cap")
	}
	if !strings.Contains(html, "old-11") || !strings.Contains(html, "old-2") {
		t.Fatal("runs panel missing recent finished runs")
	}
	if !strings.Contains(html, "live") {
		t.Fatal("runs panel missing the running run")
	}
	if !strings.Contains(html, "exit 11") {
		t.Fatal("finished run missing exit code")
	}
	if !strings.Contains(html, "st-failed") {
		t.Fatal("failed run missing status class")
	}
}

func TestReconcilePublishesRunEvent(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{alive: map[spawn.Handle]bool{}} // every handle reads dead
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "ghost")

	ev, cancel := testSrv.hub.Subscribe()
	defer cancel()
	if err := testSrv.Reconcile(); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := st.RunByID(run.ID)
	if !ok || got.Status != "failed" {
		t.Fatalf("run status = %q, want failed", got.Status)
	}
	for {
		select {
		case e := <-ev:
			if e.Event == "run" && strings.Contains(string(e.Data), `"status":"failed"`) {
				return // published
			}
		default:
			t.Fatal("no run event published by Reconcile")
		}
	}
}
