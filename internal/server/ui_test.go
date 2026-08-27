package server

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/store"
)

func TestRootRedirectsToUI(t *testing.T) {
	ts, _, _ := newTestServer(t)
	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/projects" {
		t.Fatalf("status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestStaticServed(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, p := range []string{"/static/app.css", "/static/app.js"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d", p, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestProjectsPage(t *testing.T) {
	ts, _, root := newTestServer(t)
	postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root}).Body.Close()
	resp, err := http.Get(ts.URL + "/ui/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{"erbrus", "general", "Add project", "/ui/channels/"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestProjectsPageAddForm(t *testing.T) {
	ts, st, root := newTestServer(t)
	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(ts.URL+"/ui/projects", url.Values{"repo_path": {root}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	projects, _ := st.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}
}

func TestProjectsPageWarningBanner(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/ui/projects?warning=xyz")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, `<div class="banner">xyz</div>`) {
		t.Errorf("warning banner missing from page: %s", body)
	}
}

func TestProjectsPageAddFormErrorRerenders(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.PostForm(ts.URL+"/ui/projects", url.Values{"repo_path": {""}})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if !strings.Contains(body, "repo_path is required") {
		t.Errorf("error message missing from re-render: %q", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "Add project") {
		t.Error("re-render should still be the projects page")
	}
}

func TestChannelPage(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID),
		map[string]string{"kind": "report", "body": "the report body"}).Body.Close()
	run, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "impl-x",
		Workdir: root, Status: "starting", Spawner: "tmux"})
	st.StartRun(run.ID, "erbrus-x:1")

	resp2, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, chID))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	for _, want := range []string{
		"the report body", "REPORT", "/ui/spawn?channel=", "/ui/forward?message=",
		"impl-x", "erbrus-x:1", `id="messages"`, `id="runs"`,
		"kimi", // preset from newTestServer cfg appears in the presets rail
	} {
		if !strings.Contains(body, want) {
			t.Errorf("channel page missing %q", want)
		}
	}
}

func TestChannelPartials(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID),
		map[string]string{"kind": "message", "body": "partial body"}).Body.Close()

	sresp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/stream", ts.URL, chID))
	sbody := readBody(t, sresp)
	sresp.Body.Close()
	if !strings.Contains(sbody, "partial body") || strings.Contains(sbody, "<html") {
		t.Errorf("stream partial wrong: %q", sbody[:min(200, len(sbody))])
	}
	rresp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, chID))
	rbody := readBody(t, rresp)
	rresp.Body.Close()
	if strings.Contains(rbody, "<html") {
		t.Error("runs partial must not include the layout")
	}
}

func TestChannelComposerPost(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, chID),
		url.Values{"kind": {"message"}, "body": {"typed in the browser"}})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Body == "typed in the browser" && m.AuthorKind == "human" {
			found = true
		}
	}
	if !found {
		t.Error("composer message not stored")
	}
}

func TestUIStopRun(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:9"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	run := decode[map[string]any](t, rresp)
	runID := int64(run["id"].(float64))

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(fmt.Sprintf("%s/ui/runs/%d/stop", ts.URL, runID),
		url.Values{"channel": {fmt.Sprint(chID)}})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	got, _, _ := st.RunByID(runID)
	if got.Status != "stopped" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestAppJSCarriesSSEContract(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	for _, want := range []string{"EventSource('/events')", "/stream", "/runs-panel", "dataset.channel", "channel_id"} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
}

func TestSpawnDialogRenders(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	m, _ := st.CreateMessage(store.Message{ChannelID: chID, Kind: "report", AuthorKind: "agent", AuthorName: "impl-x", Body: "origin report body"})

	dresp, _ := http.Get(fmt.Sprintf("%s/ui/spawn?channel=%d&preset=kimi&origin=%d", ts.URL, chID, m.ID))
	body := readBody(t, dresp)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", dresp.StatusCode)
	}
	for _, want := range []string{"target_channel", "kimi", "impl-x", "origin_message_id", "Spawn agent"} {
		if !strings.Contains(body, want) {
			t.Errorf("dialog missing %q", want)
		}
	}
}

func TestSpawnDialogPost(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "ui:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(ts.URL+"/ui/spawn", url.Values{
		"target_channel": {fmt.Sprint(chID)}, "provider": {"codex"}, "prompt": {"from the dialog"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 1 || runs[0].Provider != "codex" {
		t.Fatalf("runs = %+v", runs)
	}
	if len(fs.specs) != 1 {
		t.Fatal("spawner not called")
	}
}

func TestSpawnDialogPostErrorRerenders(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "ui:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	resp2, err := http.PostForm(ts.URL+"/ui/spawn", url.Values{
		"target_channel": {fmt.Sprint(chID)}, "provider": {"nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp2.StatusCode)
	}
	if !strings.Contains(body, "nope") {
		t.Error("error page should echo the bad provider")
	}
}

func TestForwardDialogAndPost(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))
	m, _ := st.CreateMessage(store.Message{ChannelID: src, Kind: "report", AuthorKind: "human", AuthorName: "you", Body: "fwd me"})

	dresp, _ := http.Get(fmt.Sprintf("%s/ui/forward?message=%d", ts.URL, m.ID))
	body := readBody(t, dresp)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK || !strings.Contains(body, "fwd me") || !strings.Contains(body, `name="target"`) {
		t.Fatalf("dialog status=%d body missing pieces", dresp.StatusCode)
	}

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(ts.URL+"/ui/forward", url.Values{
		"message_id": {fmt.Sprint(m.ID)}, "target": {fmt.Sprintf("c%d", dst)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	msgs, _ := st.MessagesSince(dst, 0, 100)
	found := false
	for _, mm := range msgs {
		if mm.Body == "fwd me" && mm.OriginMessageID == m.ID {
			found = true
		}
	}
	if !found {
		t.Error("forwarded copy not in target channel")
	}
}

func TestForwardedMessageShowsProvenanceAndArtifacts(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", "report")
	w.WriteField("body", "the source report")
	fw, _ := w.CreateFormFile("file", "provenance.md")
	io.WriteString(fw, "# findings")
	w.Close()
	mresp, err := http.Post(fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src), w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	fresp := postJSON(t, fmt.Sprintf("%s/api/messages/%d/forward", ts.URL, msgID), map[string]int64{"channel_id": dst})
	fresp.Body.Close()

	projectName := p["name"].(string)

	dresp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, dst))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, dresp)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", dresp.StatusCode)
	}
	for _, want := range []string{"the source report", projectName, "provenance.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("target channel page missing %q: %s", want, body)
		}
	}
}

func TestSettingsRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ERBRUS_HOME", tmp)
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 7420\n"), 0o644)

	ts, _, root := newTestServer(t)
	testSrv.SetConfigPath(cfgPath)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	resp.Body.Close()

	gresp, _ := http.Get(ts.URL + "/ui/settings")
	body := readBody(t, gresp)
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", gresp.StatusCode)
	}
	for _, want := range []string{"restarting", "port: 7420", ".erbrus.yaml"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := c.PostForm(ts.URL+"/ui/settings/global", url.Values{"content": {"port: 9999\n"}})
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Fatalf("valid save status = %d", r2.StatusCode)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "9999") {
		t.Errorf("global config not written: %q", data)
	}

	r3, _ := http.PostForm(ts.URL+"/ui/settings/global", url.Values{"content": {"port: [broken"}})
	b3 := readBody(t, r3)
	r3.Body.Close()
	if r3.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save status = %d, want 422", r3.StatusCode)
	}
	if !strings.Contains(b3, "yaml") && !strings.Contains(b3, "parse") {
		t.Error("error page should mention the parse failure")
	}
	data, _ = os.ReadFile(cfgPath)
	if strings.Contains(string(data), "broken") {
		t.Error("invalid YAML must not be written")
	}
}

func TestSettingsRepoSave(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ERBRUS_HOME", tmp)
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	resp.Body.Close()
	projects, _ := st.Projects()

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := c.PostForm(fmt.Sprintf("%s/ui/settings/repo?project=%d", ts.URL, projects[0].ID),
		url.Values{"content": {"session: from-ui\n"}})
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", r2.StatusCode)
	}
	repoCfg, legacy, err := config.LoadRepo(projects[0].RepoPath)
	if err != nil || legacy || repoCfg.Session != "from-ui" {
		t.Errorf("repo config not written to new location: %+v legacy=%v err=%v", repoCfg, legacy, err)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddProjectNonexistentPathIs400(t *testing.T) {
	ts, st, _ := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": `t:\definitely\missing\dir`})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	projects, _ := st.Projects()
	if len(projects) != 0 {
		t.Errorf("no project row must be created, got %d", len(projects))
	}
}

func TestProjectCardNoGitBadgeAndInit(t *testing.T) {
	ts, st, root := newTestServer(t)
	// root is a plain temp dir with no .git — the card must flag it.
	postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root}).Body.Close()

	resp, err := http.Get(ts.URL + "/ui/projects")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	if !strings.Contains(body, "no git repo") || !strings.Contains(body, "git-init") {
		t.Errorf("card missing no-git badge or init button:\n%s", body)
	}

	projects, _ := st.Projects()
	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := c.PostForm(fmt.Sprintf("%s/ui/projects/%d/git-init", ts.URL, projects[0].ID), url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Fatalf("git-init status = %d, want 302", r2.StatusCode)
	}
}
