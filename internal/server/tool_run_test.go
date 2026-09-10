package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// withShell adds the built-in shell provider/preset to the test server
// (newTestServer replaces cfg.Providers wholesale).
func withShell(t *testing.T) {
	t.Helper()
	testSrv.rulesMu.Lock()
	testSrv.cfg.Providers["shell"] = config.ShellProvider()
	testSrv.cfg.Presets["shell"] = config.ShellPreset()
	testSrv.rulesMu.Unlock()
}

func readCmdSh(t *testing.T, dataDir string, id int64) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "runs", fmt.Sprint(id), "cmd.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func getBody(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// firstMessageID posts a memo so the forward dialog has a real source.
func firstMessageID(t *testing.T, st *store.Store, ch int64) string {
	t.Helper()
	m, err := st.CreateMessage(store.Message{ChannelID: ch, Kind: "message", AuthorKind: "human", AuthorName: "h", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(m.ID)
}

func TestToolRunSpawnsBareShell(t *testing.T) {
	ts, st, root := newTestServer(t)
	withShell(t)
	fs := &fakeSpawner{handle: "s:9"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)

	resp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "preset": "shell", "prompt": "ignored"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("spawn: %d", resp.StatusCode)
	}
	rj := decode[map[string]any](t, resp)
	id := int64(rj["id"].(float64))
	run, _, _ := st.RunByID(id)
	if run.Provider != "shell" || run.Prompt != "" || run.Workdir != root {
		t.Fatalf("run = %+v", run)
	}
	if len(fs.specs) != 1 || fs.specs[0].WindowName != filepath.Base(root)+"/shell" {
		t.Fatalf("specs = %+v", fs.specs)
	}
	// cmd.sh holds the bare shell: no preamble, no hook args, no prompt.
	cmd := readCmdSh(t, testSrv.dataDir, id)
	if !strings.Contains(cmd, "exec ${SHELL:-bash}") || strings.Contains(cmd, "erbrus msg") || strings.Contains(cmd, "ignored") {
		t.Fatalf("cmd.sh = %q", cmd)
	}
	if len(fs.sent) != 0 {
		t.Fatalf("pasted into a shell: %v", fs.sent)
	}
}

func TestToolRunIsNotClassifiedAndNotATarget(t *testing.T) {
	ts, st, root := newTestServer(t)
	withShell(t)
	fs := &fakeSpawner{handle: "s:9", alive: map[spawn.Handle]bool{"s:9": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	resp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "preset": "shell"})
	id := int64(decode[map[string]any](t, resp)["id"].(float64))

	// A screen that any agent rule set would call "working" changes nothing.
	fs.setScreen("s:9", spawn.Screen{Raw: "Working (3s · esc to interrupt)\n", Activity: time.Now().Add(-10 * time.Minute)})
	if err := testSrv.WatchScreens(); err != nil {
		t.Fatal(err)
	}
	if _, ok := testSrv.stateOf(id); ok {
		t.Fatal("tool run was classified")
	}

	page := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if strings.Contains(page, fmt.Sprintf(`<option value="r%d">`, id)) {
		t.Fatal("shell offered as composer target")
	}
	if !strings.Contains(page, `class="badge tool"`) {
		t.Fatal("no shell badge on the run card")
	}
	fwd := getBody(t, ts.URL+"/ui/forward?message="+firstMessageID(t, st, ch1))
	if strings.Contains(fwd, fmt.Sprintf(`value="r%d"`, id)) {
		t.Fatal("shell offered as forward target")
	}

	run, _, _ := st.RunByID(id)
	if w, queued := testSrv.sendToRun(run, "hello"); queued || !strings.Contains(w, "is a shell, not an agent") {
		t.Fatalf("sendToRun = %q %v", w, queued)
	}
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"target": {fmt.Sprintf("r%d", id)}, "body": {"hi"}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !strings.Contains(r.Header.Get("Location"), "warning=") || len(fs.sent) != 0 {
		t.Fatalf("composer typed into the shell: loc=%s sent=%v", r.Header.Get("Location"), fs.sent)
	}
}
