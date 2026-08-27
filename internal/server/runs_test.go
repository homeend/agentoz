package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"erbrus/internal/spawn"
)

type fakeSpawner struct {
	specs  []spawn.RunSpec
	handle spawn.Handle
	err    error
	killed []spawn.Handle
	alive  map[spawn.Handle]bool
}

func (f *fakeSpawner) Spawn(s spawn.RunSpec) (spawn.Handle, error) {
	f.specs = append(f.specs, s)
	return f.handle, f.err
}
func (f *fakeSpawner) Stop(h spawn.Handle) error          { f.killed = append(f.killed, h); return nil }
func (f *fakeSpawner) Alive(h spawn.Handle) (bool, error) { return f.alive[h], nil }

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
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	// No SetRuntime call — default server has no spawner.
	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", r.StatusCode)
	}
	r.Body.Close()
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
