package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/store"
)

const wtJSON = `[
  {"path": "%s", "branch": "refs/heads/main", "head": "abc", "is_main": true},
  {"path": "%s", "branch": "refs/heads/wt/feat", "head": "def", "is_main": false}
]`

// newTestServer returns the server, its store, and a fake repo root whose
// worktree listing contains main + one worktree.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()
	wtOut := fmt.Sprintf(wtJSON, root, root+"-wt-feat")
	run := func(dir, name string, args ...string) ([]byte, error) {
		if name == "wt" {
			return []byte(wtOut), nil
		}
		return nil, fmt.Errorf("unexpected exec %s", name)
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	srv := New(st, cfg, run)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, root
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAddProjectCreatesChannels(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	p := decode[map[string]any](t, resp)
	if p["name"] != filepath.Base(root) {
		t.Errorf("name = %v", p["name"])
	}
	chans := p["channels"].([]any)
	if len(chans) != 2 {
		t.Fatalf("channels = %d, want 2 (general + wt/feat)", len(chans))
	}
	names := []string{
		chans[0].(map[string]any)["name"].(string),
		chans[1].(map[string]any)["name"].(string),
	}
	if names[0] != "general" || names[1] != "wt/feat" {
		t.Errorf("channel names = %v", names)
	}
}

func TestAddProjectIdempotent(t *testing.T) {
	ts, _, root := newTestServer(t)
	postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root}).Body.Close()
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second add status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	listResp, _ := http.Get(ts.URL + "/api/projects")
	projects := decode[[]map[string]any](t, listResp)
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}
}

func TestAddProjectDetectionFailureStillCreates(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	failRun := func(dir, name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	srv := New(st, cfg, failRun)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	root := t.TempDir()
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	if p["warning"] == nil || p["warning"] == "" {
		t.Error("want warning in response when detection fails")
	}
	if len(p["channels"].([]any)) != 1 {
		t.Error("want general channel only")
	}
}

func TestPostAndReadMessages(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	mresp := postJSON(t, url, map[string]string{"kind": "message", "body": "hello"})
	if mresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", mresp.StatusCode)
	}
	m := decode[map[string]any](t, mresp)
	if m["author_kind"] != "human" {
		t.Errorf("no-token post should be human, got %v", m["author_kind"])
	}

	getResp, _ := http.Get(url + "?since=0&limit=10")
	msgs := decode[[]map[string]any](t, getResp)
	if len(msgs) != 1 || msgs[0]["body"] != "hello" {
		t.Fatalf("read back: %+v", msgs)
	}
}

func TestPostMessageWithRunToken(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	run, err := st.CreateRun(store.AgentRun{
		ChannelID: chID, Provider: "codex", AgentName: "impl-codex",
		Workdir: root, Status: "running", Spawner: "tmux",
	})
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	b, _ := json.Marshal(map[string]string{"kind": "report", "body": "done"})
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+run.Token)
	mresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	m := decode[map[string]any](t, mresp)
	if m["author_kind"] != "agent" || m["author_name"] != "impl-codex" {
		t.Errorf("agent attribution wrong: %+v", m)
	}

	req2, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer bogus")
	r2, _ := http.DefaultClient.Do(req2)
	if r2.StatusCode != http.StatusUnauthorized {
		t.Errorf("bogus token status = %d, want 401", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestPostMessageMultipartWithFile(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", "report")
	w.WriteField("body", "with artifact")
	fw, _ := w.CreateFormFile("file", "analysis.md")
	io.WriteString(fw, "# findings")
	w.Close()

	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	mresp, err := http.Post(url, w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if mresp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(mresp.Body)
		t.Fatalf("status = %d: %s", mresp.StatusCode, body)
	}
	m := decode[map[string]any](t, mresp)
	arts := m["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(arts))
	}
	if arts[0].(map[string]any)["filename"] != "analysis.md" {
		t.Errorf("artifact: %+v", arts[0])
	}
	if int64(arts[0].(map[string]any)["size"].(float64)) != int64(len("# findings")) {
		t.Error("artifact size mismatch")
	}
}

func TestValidation(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty repo_path status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
	resp2 := postJSON(t, ts.URL+"/api/channels/999/messages", map[string]string{"kind": "message", "body": "x"})
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unknown channel status = %d, want 404", resp2.StatusCode)
	}
	resp2.Body.Close()
}
