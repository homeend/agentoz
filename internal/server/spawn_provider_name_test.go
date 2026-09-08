package server

import (
	"net/http"
	"testing"
)

// `erbrus start <name>` sends the name as a preset; a provider name that
// is no preset must spawn that provider with its defaults.
func TestSpawnPresetFieldAcceptsProviderName(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "preset": "codex"})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", r.StatusCode)
	}
	run := decode[map[string]any](t, r)
	if run["provider"] != "codex" || run["agent_name"] != "codex" {
		t.Fatalf("run = %v", run)
	}

	// An unknown name is still an unknown preset.
	r = postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "preset": "nope"})
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown name: status = %d, want 400", r.StatusCode)
	}
	r.Body.Close()
}
