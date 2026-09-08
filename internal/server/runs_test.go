package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

type fakeSpawner struct {
	specs   []spawn.RunSpec
	handle  spawn.Handle
	err     error
	killed  []spawn.Handle
	alive   map[spawn.Handle]bool
	sent    []string // "handle|text" per Send call
	keys    []string // "handle|key" per SendKeys call
	sendErr error
	// screens scripts Capture per handle; captureErr wins when set.
	// capMu guards them: the screen feed polls from a goroutine.
	capMu      sync.Mutex
	screens    map[spawn.Handle]spawn.Screen
	captureErr error
	captures   int
}

func (f *fakeSpawner) SendKeys(h spawn.Handle, key string) error {
	f.keys = append(f.keys, string(h)+"|"+key)
	return nil
}

func (f *fakeSpawner) Capture(h spawn.Handle) (spawn.Screen, error) {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	f.captures++
	if f.captureErr != nil {
		return spawn.Screen{}, f.captureErr
	}
	return f.screens[h], nil
}

// setScreen swaps the scripted screen for h (safe to call while polled).
func (f *fakeSpawner) setScreen(h spawn.Handle, sc spawn.Screen) {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	if f.screens == nil {
		f.screens = map[spawn.Handle]spawn.Screen{}
	}
	f.screens[h] = sc
}

func (f *fakeSpawner) Spawn(s spawn.RunSpec) (spawn.Handle, error) {
	f.specs = append(f.specs, s)
	return f.handle, f.err
}
func (f *fakeSpawner) Stop(h spawn.Handle) error          { f.killed = append(f.killed, h); return nil }
func (f *fakeSpawner) Alive(h spawn.Handle) (bool, error) { return f.alive[h], nil }
func (f *fakeSpawner) Send(h spawn.Handle, text string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.capMu.Lock() // deliverPrompt calls Send from its own goroutine
	defer f.capMu.Unlock()
	f.sent = append(f.sent, string(h)+"|"+text)
	return nil
}

func TestSpawnRunTmux(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	fs := &fakeSpawner{handle: "erbrus-x:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL) // see note below on exposing the server

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "provider": "codex", "prompt": "do it",
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	run := decode[map[string]any](t, rresp)
	if run["status"] != "running" || run["tmux_target"] != "erbrus-x:1" {
		t.Fatalf("run = %v", run)
	}

	if len(fs.specs) != 1 {
		t.Fatal("spawner not called")
	}
	spec := fs.specs[0]
	if len(spec.Command) != 3 || spec.Command[0] != "/abs/erbrus" || spec.Command[1] != "wrap" {
		t.Errorf("command = %v", spec.Command)
	}
	data, err := os.ReadFile(spec.Command[2])
	if err != nil {
		t.Fatalf("cmd.sh unreadable: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "#!/bin/sh\nexec codex ") {
		t.Errorf("cmd.sh = %q", content)
	}
	if !strings.Contains(content, "do it") || !strings.Contains(content, "msg send --report") {
		t.Errorf("prompt/preamble missing from command: %q", content)
	}
	if spec.Env["ERBRUS_URL"] != ts.URL || spec.Env["ERBRUS_CHANNEL"] != fmt.Sprint(chID) {
		t.Errorf("env = %v", spec.Env)
	}
	if spec.Env["ERBRUS_TOKEN"] == "" || spec.Env["ERBRUS_RUN_ID"] == "" {
		t.Errorf("token/run id env missing: %v", spec.Env)
	}
	if spec.Session != "erbrus-"+p["name"].(string) {
		t.Errorf("session = %q", spec.Session)
	}

	// A system message announced the spawn.
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Kind == "system" && strings.Contains(m.Body, "spawned in tmux erbrus-x:1") {
			found = true
		}
	}
	if !found {
		t.Errorf("no spawn system message: %+v", msgs)
	}
}

func TestSpawnRunPresetAndOverrides(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "preset": "kimi", "model": "k3-override",
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	run := decode[map[string]any](t, rresp)
	if run["provider"] != "kimi" || run["agent_name"] != "kimi" {
		t.Errorf("preset not applied: %v", run)
	}
	data, _ := os.ReadFile(fs.specs[0].Command[2])
	if !strings.Contains(string(data), "k3-override") {
		t.Errorf("model override not rendered: %q", data)
	}
}

func TestSpawnRunValidation(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	for name, body := range map[string]map[string]any{
		"unknown channel":  {"channel_id": 999, "provider": "codex"},
		"no provider":      {"channel_id": chID},
		"unknown preset":   {"channel_id": chID, "preset": "nope"},
		"unknown provider": {"channel_id": chID, "provider": "nope"},
	} {
		r := postJSON(t, ts.URL+"/api/runs", body)
		if r.StatusCode != http.StatusBadRequest && r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 400/404", name, r.StatusCode)
		}
		r.Body.Close()
	}
}

func TestSpawnRunNoSpawnerIs503(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	// No SetRuntime call — default server has no spawner.
	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", r.StatusCode)
	}
	r.Body.Close()

	// The 503 must be raised before any run row is created (no orphaned
	// "starting" rows).
	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 0 {
		t.Errorf("run row created despite 503: %+v", runs)
	}
}

func TestSpawnRunUnknownOriginMessageIs404(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "provider": "codex", "origin_message_id": 99999,
	})
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", r.StatusCode)
	}
	r.Body.Close()

	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 0 {
		t.Errorf("run row created despite unknown origin message: %+v", runs)
	}
}

func TestSpawnRunInvalidModel(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "provider": "codex", "model": "x\nrm -rf /tmp",
	})
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", r.StatusCode)
	}
	r.Body.Close()

	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 0 {
		t.Errorf("run row created despite invalid model: %+v", runs)
	}
}

func TestSpawnRunInvalidArgs(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chID, "provider": "codex", "args": "$(evil)",
	})
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", r.StatusCode)
	}
	r.Body.Close()

	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 0 {
		t.Errorf("run row created despite invalid args: %+v", runs)
	}
}

func TestSpawnRunInvalidRepoConfigIs400(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	if err := os.WriteFile(filepath.Join(root, ".erbrus.yaml"), []byte("session: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", r.StatusCode)
	}
	r.Body.Close()

	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 0 {
		t.Errorf("run row created despite invalid repo config: %+v", runs)
	}
}

func TestValidModel(t *testing.T) {
	// Render single-quotes the model, so shell metacharacters are inert;
	// only a newline or NUL could break out of the quoted argument.
	valid := []string{"", "gpt-4", "k3", "claude-3.5-sonnet", "model_1:latest", "a.b.c", "Claude Opus 4.6 (Thinking)", "x; rm -rf /tmp", "$(evil)", "a'b"}
	for _, m := range valid {
		if !validModel(m) {
			t.Errorf("validModel(%q) = false, want true", m)
		}
	}
	invalid := []string{"a\nb", "a\x00b"}
	for _, m := range invalid {
		if validModel(m) {
			t.Errorf("validModel(%q) = true, want false", m)
		}
	}
}

func TestValidArgs(t *testing.T) {
	valid := []string{"", "--flag value", "-x=1 --y=2", "a/b/c", "--model=gpt-4"}
	for _, a := range valid {
		if !validArgs(a) {
			t.Errorf("validArgs(%q) = false, want true", a)
		}
	}
	invalid := []string{"$(evil)", "a;b", "a|b", "a&b", "a<b", "a>b", "`cmd`", "a\nb"}
	for _, a := range invalid {
		if validArgs(a) {
			t.Errorf("validArgs(%q) = true, want false", a)
		}
	}
}

func TestRunExitEndpoint(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	run := decode[map[string]any](t, rresp)
	runID := int64(run["id"].(float64))
	stRun, _, _ := st.RunByID(runID)

	// Wrong token: 401.
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/runs/%d/exit", ts.URL, runID),
		strings.NewReader(`{"code": 0}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	r1, _ := http.DefaultClient.Do(req)
	if r1.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", r1.StatusCode)
	}
	r1.Body.Close()

	// Right token: run finishes failed on nonzero code.
	req2, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/runs/%d/exit", ts.URL, runID),
		strings.NewReader(`{"code": 3}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+stRun.Token)
	r2, _ := http.DefaultClient.Do(req2)
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("exit status = %d", r2.StatusCode)
	}
	r2.Body.Close()
	got, _, _ := st.RunByID(runID)
	if got.Status != "failed" || !got.HasExit || got.ExitCode != 3 {
		t.Errorf("run after exit: %+v", got)
	}
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Kind == "system" && strings.Contains(m.Body, "exit 3") {
			found = true
		}
	}
	if !found {
		t.Error("no exit system message")
	}

	// Second exit with the same valid token: 409, no additional system message.
	msgsBefore, _ := st.MessagesSince(chID, 0, 100)
	req3, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/runs/%d/exit", ts.URL, runID),
		strings.NewReader(`{"code": 0}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer "+stRun.Token)
	r3, _ := http.DefaultClient.Do(req3)
	if r3.StatusCode != http.StatusConflict {
		t.Errorf("double exit status = %d, want 409", r3.StatusCode)
	}
	r3.Body.Close()
	msgsAfter, _ := st.MessagesSince(chID, 0, 100)
	if len(msgsAfter) != len(msgsBefore) {
		t.Errorf("double exit posted extra messages: before=%d after=%d", len(msgsBefore), len(msgsAfter))
	}
}

func TestRunStopEndpoint(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:2"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	run := decode[map[string]any](t, rresp)
	runID := int64(run["id"].(float64))

	r := postJSON(t, fmt.Sprintf("%s/api/runs/%d/stop", ts.URL, runID), map[string]any{})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d", r.StatusCode)
	}
	r.Body.Close()
	if len(fs.killed) != 1 || fs.killed[0] != "s:2" {
		t.Errorf("kill not issued: %v", fs.killed)
	}
	got, _, _ := st.RunByID(runID)
	if got.Status != "stopped" {
		t.Errorf("status = %q", got.Status)
	}
	// Second stop: 409.
	r2 := postJSON(t, fmt.Sprintf("%s/api/runs/%d/stop", ts.URL, runID), map[string]any{})
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("double stop status = %d, want 409", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestChannelRunsList(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"}).Body.Close()

	lr, _ := http.Get(fmt.Sprintf("%s/api/channels/%d/runs", ts.URL, chID))
	runs := decode[[]map[string]any](t, lr)
	if len(runs) != 1 || runs[0]["provider"] != "codex" {
		t.Errorf("runs = %v", runs)
	}
}

func TestSpawnFromReportAppendsContext(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, src),
		map[string]string{"kind": "report", "body": "the analysis result"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": dst, "provider": "codex", "prompt": "implement it",
		"origin_message_id": msgID,
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	rresp.Body.Close()

	data, _ := os.ReadFile(fs.specs[0].Command[2])
	if !strings.Contains(string(data), "the analysis result") {
		t.Errorf("report body not in prompt: %q", data)
	}
	// Origin channel got the handoff note.
	msgs, _ := st.MessagesSince(src, msgID, 100)
	found := false
	for _, mm := range msgs {
		if mm.Kind == "system" && strings.Contains(mm.Body, "handed off") {
			found = true
		}
	}
	if !found {
		t.Error("no handoff system note in origin channel")
	}
}

// TestSpawnFromReportNamesSourceProject regression-tests the cross-project
// handoff provenance line: it must name the project the report came FROM,
// not the project the new run is being spawned INTO.
func TestSpawnFromReportNamesSourceProject(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rootA := filepath.Join(t.TempDir(), "proj-alpha")
	rootB := filepath.Join(t.TempDir(), "proj-bravo")
	os.MkdirAll(rootA, 0o755)
	os.MkdirAll(rootB, 0o755)
	run := func(dir, name string, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true}]`, dir)), nil
	}
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Providers = map[string]config.Provider{
		"codex": {Command: `codex {args} "{prompt}"`},
	}
	srv := New(st, cfg, run)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	rA := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": rootA})
	pA := decode[map[string]any](t, rA)
	rB := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": rootB})
	pB := decode[map[string]any](t, rB)
	nameA := pA["name"].(string)
	nameB := pB["name"].(string)
	if nameA == nameB {
		t.Fatalf("test setup: project names collide: %q, %q", nameA, nameB)
	}
	chA := int64(pA["channels"].([]any)[0].(map[string]any)["id"].(float64))
	chB := int64(pB["channels"].([]any)[0].(map[string]any)["id"].(float64))

	fs := &fakeSpawner{handle: "s:1"}
	srv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	// Post a report in project A's channel, then spawn a run in project B
	// that hands off from that report.
	mresp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chA),
		map[string]string{"kind": "report", "body": "findings from alpha"})
	m := decode[map[string]any](t, mresp)
	msgID := int64(m["id"].(float64))

	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{
		"channel_id": chB, "provider": "codex", "prompt": "act on it",
		"origin_message_id": msgID,
	})
	if rresp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rresp.StatusCode)
	}
	rresp.Body.Close()

	data, err := os.ReadFile(fs.specs[0].Command[2])
	if err != nil {
		t.Fatalf("cmd.sh unreadable: %v", err)
	}
	content := string(data)
	wantSubstr := fmt.Sprintf("report from %s / #", nameA)
	if !strings.Contains(content, wantSubstr) {
		t.Errorf("provenance line missing source project %q: %q", wantSubstr, content)
	}
	badSubstr := fmt.Sprintf("report from %s / #", nameB)
	if strings.Contains(content, badSubstr) {
		t.Errorf("provenance line wrongly names target project %q: %q", nameB, content)
	}
}

func TestSpawnRunFg(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	// fg needs no spawner.
	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex", "fg": true})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", r.StatusCode)
	}
	fg := decode[map[string]any](t, r)
	if fg["cmd_file"] == "" || fg["env"].(map[string]any)["ERBRUS_TOKEN"] == "" {
		t.Errorf("fg spec incomplete: %v", fg)
	}
}

func TestReconcileMarksOrphans(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	alive, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "a",
		Workdir: root, Status: "running", Spawner: "tmux", TmuxTarget: "s:1"})
	dead, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "b",
		Workdir: root, Status: "running", Spawner: "tmux", TmuxTarget: "s:2"})
	fgRun, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "c",
		Workdir: root, Status: "starting", Spawner: "fg"})
	// Overwrite targets/status via StartRun where needed:
	st.StartRun(alive.ID, "s:1")
	st.StartRun(dead.ID, "s:2")

	fs := &fakeSpawner{alive: map[spawn.Handle]bool{"s:1": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	if err := testSrv.Reconcile(); err != nil {
		t.Fatal(err)
	}

	a, _, _ := st.RunByID(alive.ID)
	d, _, _ := st.RunByID(dead.ID)
	f, _, _ := st.RunByID(fgRun.ID)
	if a.Status != "running" {
		t.Errorf("alive run flipped: %q", a.Status)
	}
	if d.Status != "failed" {
		t.Errorf("orphan not failed: %q", d.Status)
	}
	if f.Status != "starting" {
		t.Errorf("fg run must be untouched: %q", f.Status)
	}
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Kind == "system" && strings.Contains(m.Body, "no longer alive") {
			found = true
		}
	}
	if !found {
		t.Error("no orphan system message")
	}
}
