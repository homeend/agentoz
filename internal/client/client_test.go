package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/server"
	"erbrus/internal/store"
)

func testServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()
	run := func(dir, name string, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, root)), nil
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	ts := httptest.NewServer(server.New(st, cfg, run).Handler())
	t.Cleanup(ts.Close)
	return ts, root
}

func TestClientRoundtrip(t *testing.T) {
	ts, root := testServer(t)
	c := New(ts.URL, "")

	p, err := c.AddProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Created || len(p.Channels) != 1 {
		t.Fatalf("AddProject: %+v", p)
	}
	ch := p.Channels[0].ID

	art := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(art, []byte("hello notes"), 0o644)
	sent, err := c.SendMessage(ch, "report", "did the thing", []string{art})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Kind != "report" || len(sent.Artifacts) != 1 || sent.Artifacts[0].Filename != "notes.md" {
		t.Fatalf("SendMessage: %+v", sent)
	}

	msgs, err := c.ReadMessages(ch, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Body != "did the thing" {
		t.Fatalf("ReadMessages: %+v", msgs)
	}

	list, err := c.ListProjects()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProjects: %v %+v", err, list)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:9")
	t.Setenv("ERBRUS_TOKEN", "tok")
	t.Setenv("ERBRUS_CHANNEL", "42")
	c, ch, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "http://127.0.0.1:9" || c.Token != "tok" || ch != 42 {
		t.Fatalf("FromEnv: %+v ch=%d", c, ch)
	}
	t.Setenv("ERBRUS_URL", "")
	c, _, _ = FromEnv()
	if c.Base != "http://127.0.0.1:7420" {
		t.Errorf("default base = %q", c.Base)
	}
}

func TestSendMessageServerError(t *testing.T) {
	ts, _ := testServer(t)
	c := New(ts.URL, "")
	if _, err := c.SendMessage(999, "message", "x", nil); err == nil {
		t.Fatal("want error for unknown channel")
	}
}

func TestReportExit(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok123")
	if err := c.ReportExit(7, 3); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/runs/7/exit" || gotAuth != "Bearer tok123" || gotBody["code"] != 3 {
		t.Errorf("path=%q auth=%q body=%v", gotPath, gotAuth, gotBody)
	}
}
