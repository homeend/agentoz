package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

func doJSON(t *testing.T, method, url string, in any) (*http.Response, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	if in != nil {
		json.NewEncoder(&body).Encode(in)
	}
	req, _ := http.NewRequest(method, url, &body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestAPIRunScreen(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	var raw strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&raw, "line %d\n", i)
	}
	raw.WriteString("Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n")
	fs.setScreen("s:5", spawn.Screen{Raw: raw.String(), Cols: 100, Rows: 40, Activity: time.Now()})

	resp, out := doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen", ts.URL, run.ID), nil)
	if resp.StatusCode != 200 || out["state"] != "question" || len(out["lines"].([]any)) != 15 || out["cols"].(float64) != 100 {
		t.Fatalf("screen: %d %v", resp.StatusCode, out)
	}
	if opts := out["options"].([]any); len(opts) != 2 || opts[0].(map[string]any)["label"] != "Yes" {
		t.Fatalf("options: %v", out["options"])
	}
	_, out = doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen?lines=25", ts.URL, run.ID), nil)
	if n := len(out["lines"].([]any)); n != 25 {
		t.Fatalf("lines=25 gave %d", n)
	}
	_, out = doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen?lines=9999", ts.URL, run.ID), nil)
	if n := len(out["lines"].([]any)); n != 34 { // capped at 200; the screen has 34 non-empty lines
		t.Fatalf("lines cap gave %d", n)
	}
	noWin, _ := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "claude-code", AgentName: "x", Workdir: "/w", Status: "starting", Spawner: "fg"})
	resp, _ = doJSON(t, "GET", fmt.Sprintf("%s/api/runs/%d/screen", ts.URL, noWin.ID), nil)
	if resp.StatusCode != 404 {
		t.Fatalf("no window: %d", resp.StatusCode)
	}
}

func TestAPIScreenTestAndProviderPut(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("# mine\nport: 7420\n"), 0o644)
	testSrv.SetConfigPath(cfgPath)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	fs.setScreen("s:5", spawn.Screen{Raw: "please CONFIRM\n", Activity: time.Now()})

	// Test with unsaved rules for a provider that does not exist yet.
	resp, out := doJSON(t, "POST", ts.URL+"/api/providers/newcli/screen-test", map[string]any{"run": run.ID, "question": []string{"CONFIRM"}})
	if resp.StatusCode != 200 || out["state"] != "question" {
		t.Fatalf("screen-test: %d %v", resp.StatusCode, out)
	}
	if l := out["lines"].([]any)[0].(map[string]any); l["match"] != "question" {
		t.Fatalf("line match: %v", l)
	}
	resp, _ = doJSON(t, "POST", ts.URL+"/api/providers/newcli/screen-test", map[string]any{"run": run.ID, "working": []string{"("}})
	if resp.StatusCode != 422 {
		t.Fatalf("bad pattern status = %d", resp.StatusCode)
	}

	// PUT creates the provider, writes the file, applies live.
	resp, out = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"command": "newcli {args} \"{prompt}\"", "prompt": "arg", "screen_question": []string{"CONFIRM"}})
	if resp.StatusCode != 200 || out["command"] != "newcli {args} \"{prompt}\"" || out["rules_source"] != "config override" {
		t.Fatalf("put: %d %v", resp.StatusCode, out)
	}
	doc, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(doc), "# mine") || !strings.Contains(string(doc), "  newcli:") || !strings.Contains(string(doc), "screen_question: ['CONFIRM']") {
		t.Fatalf("config:\n%s", doc)
	}
	if got := screen.Classify(testSrv.rulesFor("newcli"), []string{"CONFIRM"}); got != screen.Question {
		t.Fatalf("rules not live: %q", got)
	}
	if _, ok := testSrv.cfg.Providers["newcli"]; !ok {
		t.Fatal("provider not in the running config")
	}
	// GET shows it; a partial PUT keeps other fields.
	_, out = doJSON(t, "GET", ts.URL+"/api/providers/newcli", nil)
	if out["prompt"] != "arg" || out["command"] == "" {
		t.Fatalf("get: %v", out)
	}
	_, out = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"default_model": "m1"})
	if out["command"] == "" || out["default_model"] != "m1" {
		t.Fatalf("partial put: %v", out)
	}
	resp, _ = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"screen_working": []string{"("}})
	if resp.StatusCode != 422 {
		t.Fatalf("bad pattern put = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, "PUT", ts.URL+"/api/providers/newcli", map[string]any{"prompt": "maybe"})
	if resp.StatusCode != 422 {
		t.Fatalf("bad prompt mode put = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, "GET", ts.URL+"/api/providers/none", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown provider get = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, "PUT", ts.URL+"/api/providers/blank", map[string]any{"prompt": "arg"})
	if resp.StatusCode != 422 {
		t.Fatalf("new provider without command = %d", resp.StatusCode)
	}
}
