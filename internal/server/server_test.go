package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/store"
)

const wtJSON = `[
  {"path": "%s", "branch": "refs/heads/main", "head": "abc", "is_main": true},
  {"path": "%s", "branch": "refs/heads/wt/feat", "head": "def", "is_main": false}
]`

// testSrv holds the last *Server built by newTestServer, so tests can reach
// SetRuntime (not exposed via the HTTP API).
var testSrv *Server

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
	cfg.Providers = map[string]config.Provider{
		"codex": {Command: `codex {args} "{prompt}"`},
		"kimi":  {Command: `kimi --model {model} {args} "{prompt}"`},
	}
	cfg.Presets = map[string]config.Preset{
		"kimi": {Provider: "kimi", Model: "k3"},
	}
	srv := New(st, cfg, run)
	testSrv = srv
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

func TestPostMessageBadKind(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	url := fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID)
	mresp := postJSON(t, url, map[string]string{"kind": "bogus", "body": "x"})
	if mresp.StatusCode != http.StatusBadRequest {
		t.Errorf("bogus kind status = %d, want 400", mresp.StatusCode)
	}
	mresp.Body.Close()
}

func TestAddProjectNameCollisionSuffixes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rootA := filepath.Join(t.TempDir(), "webshop")
	rootB := filepath.Join(t.TempDir(), "webshop") // same basename, different parent
	os.MkdirAll(rootA, 0o755)
	os.MkdirAll(rootB, 0o755)
	run := func(dir, name string, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, dir)), nil
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	ts := httptest.NewServer(New(st, cfg, run).Handler())
	defer ts.Close()

	r1 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": rootA})
	p1 := decode[map[string]any](t, r1)
	r2 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": rootB})
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("second same-name project status = %d", r2.StatusCode)
	}
	p2 := decode[map[string]any](t, r2)
	if p1["name"] != "webshop" || p2["name"] != "webshop-2" {
		t.Errorf("names = %v, %v; want webshop, webshop-2", p1["name"], p2["name"])
	}
}

func TestAddProjectRedetectsNewWorktrees(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	// Mutable worktree listing: starts with main only, grows a worktree later.
	listing := fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, root)
	run := func(dir, name string, args ...string) ([]byte, error) { return []byte(listing), nil }
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	ts := httptest.NewServer(New(st, cfg, run).Handler())
	defer ts.Close()

	r1 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p1 := decode[map[string]any](t, r1)
	if n := len(p1["channels"].([]any)); n != 1 {
		t.Fatalf("initial channels = %d, want 1", n)
	}

	listing = fmt.Sprintf(`[
	  {"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true},
	  {"path": %q, "branch": "refs/heads/wt/feat", "head": "b", "is_main": false}
	]`, root, root+"-wt-feat")

	r2 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p2 := decode[map[string]any](t, r2)
	chans := p2["channels"].([]any)
	if len(chans) != 2 {
		t.Fatalf("after re-detection channels = %d, want 2", len(chans))
	}
	if chans[1].(map[string]any)["name"] != "wt/feat" {
		t.Errorf("new channel = %v", chans[1])
	}
	// Third call: no duplicates.
	r3 := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p3 := decode[map[string]any](t, r3)
	if len(p3["channels"].([]any)) != 2 {
		t.Error("re-detection must not duplicate channels")
	}
}

func TestArtifactDownload(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", "report")
	w.WriteField("body", "with artifact")
	fw, _ := w.CreateFormFile("file", "dl.md")
	io.WriteString(fw, "download me")
	w.Close()
	mresp, err := http.Post(fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID), w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	m := decode[map[string]any](t, mresp)
	artID := int64(m["artifacts"].([]any)[0].(map[string]any)["id"].(float64))

	dresp, err := http.Get(fmt.Sprintf("%s/api/artifacts/%d", ts.URL, artID))
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", dresp.StatusCode)
	}
	body, _ := io.ReadAll(dresp.Body)
	if string(body) != "download me" {
		t.Errorf("body = %q", body)
	}
	if cd := dresp.Header.Get("Content-Disposition"); !strings.Contains(cd, "dl.md") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// Path-safety: a doctored row pointing outside the artifacts dir is 404.
	msgID := int64(m["id"].(float64))
	bad, err := st.AddArtifact(store.Artifact{MessageID: msgID, Filename: "evil", Path: "/etc/passwd", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	bresp, _ := http.Get(fmt.Sprintf("%s/api/artifacts/%d", ts.URL, bad.ID))
	if bresp.StatusCode != http.StatusNotFound {
		t.Errorf("doctored path status = %d, want 404", bresp.StatusCode)
	}
	bresp.Body.Close()

	// Path-safety: a doctored row pointing at a directory (not a regular
	// file) inside the artifacts tree is also 404, not a directory listing.
	realArt, ok, err := st.ArtifactByID(artID)
	if err != nil || !ok {
		t.Fatalf("ArtifactByID(%d): %v %v", artID, err, ok)
	}
	dirBad, err := st.AddArtifact(store.Artifact{MessageID: msgID, Filename: "dir", Path: filepath.Dir(realArt.Path), Size: 0})
	if err != nil {
		t.Fatal(err)
	}
	dresp2, _ := http.Get(fmt.Sprintf("%s/api/artifacts/%d", ts.URL, dirBad.ID))
	if dresp2.StatusCode != http.StatusNotFound {
		t.Errorf("directory path status = %d, want 404", dresp2.StatusCode)
	}
	dresp2.Body.Close()

	nresp, _ := http.Get(ts.URL + "/api/artifacts/424242")
	if nresp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown artifact status = %d, want 404", nresp.StatusCode)
	}
	nresp.Body.Close()
}

func TestOriginGuard(t *testing.T) {
	ts, _, root := newTestServer(t)

	postWithOrigin := func(origin string) *http.Response {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"repo_path": root})
		req, _ := http.NewRequest("POST", ts.URL+"/api/projects", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	evil := postWithOrigin("https://evil.example")
	evil.Body.Close()
	if evil.StatusCode != http.StatusForbidden {
		t.Errorf("evil origin status = %d, want 403", evil.StatusCode)
	}

	local := postWithOrigin("http://127.0.0.1:7420")
	local.Body.Close()
	if local.StatusCode != http.StatusOK {
		t.Errorf("local origin status = %d, want 200", local.StatusCode)
	}

	none := postWithOrigin("")
	none.Body.Close()
	if none.StatusCode != http.StatusOK {
		t.Errorf("no origin status = %d, want 200", none.StatusCode)
	}
}

func TestIsNameCollision(t *testing.T) {
	nameErr := errors.New("constraint failed: UNIQUE constraint failed: projects.name (2067)")
	if !isNameCollision(nameErr) {
		t.Error("want true for a projects.name UNIQUE constraint error")
	}
	otherErr := errors.New("constraint failed: UNIQUE constraint failed: projects.repo_path (2067)")
	if isNameCollision(otherErr) {
		t.Error("want false for a projects.repo_path UNIQUE constraint error (not a name collision)")
	}
	transientErr := errors.New("database is locked")
	if isNameCollision(transientErr) {
		t.Error("want false for a non-collision store error (must not be retried as a suffix)")
	}
	if isNameCollision(nil) {
		t.Error("want false for nil error")
	}
}
