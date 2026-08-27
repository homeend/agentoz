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

// seedDeletable adds a project via the API and gives it a running run
// (with a tmux target) plus a report message owning an on-disk artifact.
// Returns the project ID, run and message so tests can assert on them.
func seedDeletable(t *testing.T, tsURL string, st *store.Store, root string) (projectID int64, run store.AgentRun, msg store.Message) {
	t.Helper()
	resp := postJSON(t, tsURL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	projectID = int64(p["id"].(float64))
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	run, err := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex",
		AgentName: "codex", Workdir: root, Status: "running", Spawner: "tmux", TmuxTarget: "s:9"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err = st.CreateMessage(store.Message{ChannelID: chID, Kind: "report",
		AuthorKind: "agent", AuthorName: "codex", AgentRunID: run.ID, Body: "done"})
	if err != nil {
		t.Fatal(err)
	}
	artDir := filepath.Join(testSrv.dataDir, "artifacts", fmt.Sprint(msg.ID))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artPath := filepath.Join(artDir, "out.txt")
	if err := os.WriteFile(artPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddArtifact(store.Artifact{MessageID: msg.ID, Filename: "out.txt", Path: artPath, Size: 1}); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(testSrv.dataDir, "runs", fmt.Sprint(run.ID))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return projectID, run, msg
}

func TestAPIDeleteProject(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:9"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	projectID, run, msg := seedDeletable(t, ts.URL, st, root)

	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/api/projects/%d", ts.URL, projectID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[map[string]any](t, resp)
	if body["deleted"] != true {
		t.Fatalf("body = %v", body)
	}
	if _, ok, _ := st.ProjectByID(projectID); ok {
		t.Fatal("project row survived")
	}
	if len(fs.killed) != 1 || fs.killed[0] != spawn.Handle("s:9") {
		t.Fatalf("running agent not stopped: killed=%v", fs.killed)
	}
	if _, err := os.Stat(filepath.Join(testSrv.dataDir, "artifacts", fmt.Sprint(msg.ID))); !os.IsNotExist(err) {
		t.Fatalf("artifact dir survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testSrv.dataDir, "runs", fmt.Sprint(run.ID))); !os.IsNotExist(err) {
		t.Fatalf("run dir survived: %v", err)
	}
}

func TestAPIDeleteProjectMissing(t *testing.T) {
	ts, _, _ := newTestServer(t)
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/projects/999", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUIDeleteConfirmPage(t *testing.T) {
	ts, st, root := newTestServer(t)
	projectID, _, _ := seedDeletable(t, ts.URL, st, root)

	resp, err := http.Get(fmt.Sprintf("%s/ui/projects/%d/delete", ts.URL, projectID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{
		filepath.Base(root),        // project name
		"2 channels",               // general + worktree channel
		"1 messages",               // the report
		"1 artifacts",              //
		"1 runs",                   //
		"1 agent(s) still running", // active-run warning
		fmt.Sprintf("/ui/projects/%d/delete", projectID), // confirm form target
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("confirm page missing %q:\n%s", want, html)
		}
	}
}

func TestUIDeleteProjectFlow(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:9"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	projectID, _, _ := seedDeletable(t, ts.URL, st, root)

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(fmt.Sprintf("%s/ui/projects/%d/delete", ts.URL, projectID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/ui/projects") {
		t.Fatalf("redirect = %q", loc)
	}
	if _, ok, _ := st.ProjectByID(projectID); ok {
		t.Fatal("project row survived")
	}
	if len(fs.killed) != 1 {
		t.Fatalf("running agent not stopped: killed=%v", fs.killed)
	}
}

func TestProjectCardHasDeleteLink(t *testing.T) {
	ts, st, root := newTestServer(t)
	projectID, _, _ := seedDeletable(t, ts.URL, st, root)

	resp, err := http.Get(ts.URL + "/ui/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if want := fmt.Sprintf("/ui/projects/%d/delete", projectID); !strings.Contains(html, want) {
		t.Fatalf("projects page missing delete link %q", want)
	}
}
