package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

func TestUIDeleteRun(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	run, _ := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex",
		AgentName: "old", Workdir: "/w", Status: "starting", Spawner: "tmux"})
	if err := st.FinishRun(run.ID, "done", 0); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(testSrv.dataDir, "runs", fmt.Sprint(run.ID))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	c := noRedirect()
	resp, err := c.PostForm(fmt.Sprintf("%s/ui/runs/%d/delete", ts.URL, run.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, ok, _ := st.RunByID(run.ID); ok {
		t.Fatal("run row survived")
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatal("run dir survived")
	}
}

func TestUIDeleteRunRefusesActive(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "live")

	c := noRedirect()
	resp, err := c.PostForm(fmt.Sprintf("%s/ui/runs/%d/delete", ts.URL, run.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if _, ok, _ := st.RunByID(run.ID); !ok {
		t.Fatal("active run must not be deleted")
	}
}

func TestRunsPanelDeleteButtonOnlyOnFinished(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	live := runningAgent(t, st, ch1, "live")
	done, _ := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex",
		AgentName: "old", Workdir: "/w", Status: "starting", Spawner: "tmux"})
	if err := st.FinishRun(done.ID, "done", 0); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, fmt.Sprintf("/ui/runs/%d/delete", done.ID)) {
		t.Fatal("finished run missing delete button")
	}
	if strings.Contains(html, fmt.Sprintf("/ui/runs/%d/delete", live.ID)) {
		t.Fatal("running run must not offer delete")
	}
}

func TestSpawnGlobalSession(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "erbrus:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	_ = st

	resp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": ch1, "provider": "codex", "prompt": "x", "session_scope": "global",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if len(fs.specs) != 1 || fs.specs[0].Session != "erbrus" {
		t.Fatalf("spawn session = %+v, want global erbrus", fs.specs)
	}
	if fs.specs[0].AttachSession != "" {
		t.Fatal("global scope must not use attach_session")
	}
}

func TestSpawnDialogSessionSelect(t *testing.T) {
	ts, _, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)

	resp, err := http.Get(fmt.Sprintf("%s/ui/spawn?channel=%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, `name="session_scope"`) {
		t.Fatal("spawn dialog missing session_scope select")
	}
	if !strings.Contains(html, "global: erbrus") {
		t.Fatal("spawn dialog missing global session label")
	}
}

func TestAttachCmdsFollowRunSessions(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	if _, err := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex",
		AgentName: "g", Workdir: "/w", Status: "running", Spawner: "tmux",
		TmuxTarget: "erbrus:3"}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, "tmux attach -t erbrus<") {
		t.Fatalf("attach rail missing session of the running agent")
	}
}

func TestSpawnWindowNamedAfterWorkdir(t *testing.T) {
	ts, _, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	_, ch2 := twoChannels(t, ts.URL, root) // ch2 = worktree channel (<root>-wt-feat)

	resp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": ch2, "provider": "codex", "prompt": "x",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	want := filepath.Base(root) + "-wt-feat"
	if len(fs.specs) != 1 || fs.specs[0].WindowName != want {
		t.Fatalf("window name = %+v, want %q", fs.specs, want)
	}
}
