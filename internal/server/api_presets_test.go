package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIPresetPutGetAndSpawn(t *testing.T) {
	ts, _, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("# mine\nproviders:\n  codex:\n    command: codex\n"), 0o644)
	testSrv.SetConfigPath(cfgPath)

	resp, _ := doJSON(t, "GET", ts.URL+"/api/presets/cx", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing preset: %d", resp.StatusCode)
	}
	resp, out := doJSON(t, "PUT", ts.URL+"/api/presets/cx", map[string]any{"provider": "nope"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown provider: %d %v", resp.StatusCode, out)
	}
	resp, out = doJSON(t, "PUT", ts.URL+"/api/presets/cx", map[string]any{"model": "m"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no provider: %d %v", resp.StatusCode, out)
	}

	// A provider whose template takes a model, so the preset's model is
	// visible in the rendered command.
	resp, out = doJSON(t, "PUT", ts.URL+"/api/providers/mcli", map[string]any{"command": "mcli --model {model} \"{prompt}\""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put provider: %d %v", resp.StatusCode, out)
	}
	resp, out = doJSON(t, "PUT", ts.URL+"/api/presets/cx", map[string]any{"provider": "mcli", "model": "gpt-x"})
	if resp.StatusCode != http.StatusOK || out["provider"] != "mcli" || out["model"] != "gpt-x" {
		t.Fatalf("put: %d %v", resp.StatusCode, out)
	}
	doc, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(doc), "# mine") || !strings.Contains(string(doc), "presets:") || !strings.Contains(string(doc), "  cx:") || !strings.Contains(string(doc), "provider: mcli") {
		t.Fatalf("config:\n%s", doc)
	}
	resp, out = doJSON(t, "GET", ts.URL+"/api/presets/cx", nil)
	if resp.StatusCode != http.StatusOK || out["model"] != "gpt-x" {
		t.Fatalf("get: %d %v", resp.StatusCode, out)
	}

	// Live: a spawn by that preset resolves to the provider and model.
	resp = postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "preset": "cx"})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("spawn by new preset: %d", r.StatusCode)
	}
	run := decode[map[string]any](t, r)
	if run["provider"] != "mcli" || run["agent_name"] != "cx" {
		t.Fatalf("run = %v", run)
	}
	// The spawn command is `erbrus wrap <cmd.sh>`; the rendered CLI line
	// with the model lives in that file.
	if len(fs.specs) == 0 || len(fs.specs[0].Command) < 3 {
		t.Fatalf("spec: %+v", fs.specs)
	}
	cmd, err := os.ReadFile(fs.specs[0].Command[2])
	if err != nil || !strings.Contains(string(cmd), "gpt-x") {
		t.Fatalf("model from preset not used: %v\n%s", err, cmd)
	}
}
