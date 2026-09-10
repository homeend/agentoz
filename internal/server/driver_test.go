package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

// withProvider sets one provider on the test server (pattern of
// paste_test.go: cfg.Providers under rulesMu) and recompiles its screen
// rules the way ReloadConfig/setRules would — New() only compiles once,
// at construction, and a provider added afterwards needs the same
// treatment spawnPasteProvider gives "kimi" or its ScreenWaiting/
// ScreenQuestion overrides are silently ignored by rulesFor.
func withProvider(t *testing.T, name string, p config.Provider) {
	t.Helper()
	testSrv.rulesMu.Lock()
	testSrv.cfg.Providers[name] = p
	testSrv.rulesMu.Unlock()
	if len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) == 0 {
		testSrv.setRules(name, nil)
		return
	}
	r, err := screen.Compile(p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion)
	if err != nil {
		t.Fatal(err)
	}
	testSrv.setRules(name, &r)
}

// fakeDriver: a fakeSpawner with Windows-style host answers, so the
// server's platform seams show up on Linux.
type fakeDriver struct {
	*fakeSpawner
	name  string
	paste bool
	open  bool
}

func (d *fakeDriver) Name() string { return d.name }
func (d *fakeDriver) WriteCommand(runDir, command string) (string, error) {
	p := filepath.Join(runDir, "cmd.json")
	return p, os.WriteFile(p, []byte("[\""+command+"\"]\n"), 0o644)
}
func (d *fakeDriver) PromptByPaste() bool          { return d.paste }
func (d *fakeDriver) AgentBin(bin string) string   { return strings.ReplaceAll(bin, `\`, "/") }
func (d *fakeDriver) ShellTool() string            { return "powershell" }
func (d *fakeDriver) Viewer() (spawn.Viewer, bool) { return nil, false }
func (d *fakeDriver) Sweep() []string              { return nil }
func (d *fakeDriver) OpenTerminal(dir string, argv []string) ([]string, bool) {
	if !d.open {
		return nil, false
	}
	return append([]string{"wezterm", "start", "--cwd", dir, "--"}, argv...), true
}

func TestSpawnThroughDriverWritesArgvFileEnvFileAndPastes(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "7@root/claude"}
	d := &fakeDriver{fakeSpawner: fs, name: "wezterm", paste: true}
	testSrv.SetDriver(d, `T:\erbrus\bin\erbrus.exe`, ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	withProvider(t, "claude-code", config.Provider{Command: `claude --model {model} "{prompt}"`, Prompt: "arg", ScreenWaiting: []string{"❯"}})
	fs.setScreen("7@root/claude", spawn.Screen{Raw: "❯ \n"}) // input box up: the paste goes out at once

	r := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "claude-code", "prompt": "do it"})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("spawn = %d", r.StatusCode)
	}
	runs, _ := st.RunsByChannel(ch1)
	run := runs[0]
	if run.Spawner != "wezterm" {
		t.Errorf("Spawner = %q", run.Spawner)
	}
	cmdFile := fs.specs[0].Command[2]
	if filepath.Base(cmdFile) != "cmd.json" {
		t.Errorf("command file = %s", cmdFile)
	}
	b, _ := os.ReadFile(cmdFile)
	if strings.Contains(string(b), "do it") || strings.Contains(string(b), "erbrus channel") {
		t.Errorf("PromptByPaste driver must keep the prompt off the command line: %s", b)
	}
	env, _ := os.ReadFile(filepath.Join(filepath.Dir(cmdFile), "env"))
	if !strings.Contains(string(env), "ERBRUS_RUN_ID="+fmt.Sprint(run.ID)) || !strings.Contains(string(env), "ERBRUS_URL="+ts.URL) {
		t.Errorf("env file = %q", env)
	}
	// The pasted preamble spells the binary with forward slashes.
	sent := waitSent(fs, 2*time.Second)
	if len(sent) == 0 {
		t.Fatal("prompt was never pasted")
	}
	if !strings.Contains(sent[0], "T:/erbrus/bin/erbrus.exe msg send") || strings.Contains(sent[0], `T:\erbrus`) {
		t.Errorf("preamble = %q", sent)
	}
}

func TestReconcileAndWatcherCoverNonTmuxRuns(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{handle: "7@root/claude", alive: map[spawn.Handle]bool{}}
	testSrv.SetDriver(&fakeDriver{fakeSpawner: fs, name: "wezterm"}, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	withProvider(t, "claude-code", config.Provider{Command: "claude"})
	postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "claude-code", "prompt": "x"})
	if err := testSrv.WatchScreens(); err != nil {
		t.Fatal(err)
	}
	if err := testSrv.Reconcile(); err != nil {
		t.Fatal(err)
	}
	runs, _ := st.RunsByChannel(ch1)
	if runs[0].Status != "failed" {
		t.Errorf("a wezterm run whose pane is gone must be failed by Reconcile, got %q", runs[0].Status)
	}
}

func TestTerminalToolLaunchesWhenDriverOpensTerminals(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetDriver(&fakeDriver{fakeSpawner: fs, name: "wezterm", open: true}, "/abs/erbrus", ts.URL)
	fl := &fakeLauncher{}
	testSrv.SetLauncher(fl)
	ch1, _ := twoChannels(t, ts.URL, root)

	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/shell", ts.URL, ch1), nil)
	r.Body.Close()
	if loc := r.Header.Get("Location"); loc != fmt.Sprintf("/ui/channels/%d", ch1) {
		t.Fatalf("redirect = %s", loc)
	}
	if len(fl.calls) != 1 || !strings.HasPrefix(fl.calls[0], root+"|wezterm start --cwd "+root+" -- powershell") {
		t.Fatalf("launch = %v", fl.calls)
	}
	if runs, _ := st.RunsByChannel(ch1); len(runs) != 0 {
		t.Error("no tool run when the driver opens a window")
	}
	page := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if strings.Contains(page, fmt.Sprintf(`target="tool:shell:%d"`, ch1)) {
		t.Error("launch-style terminal tools need no tab target")
	}
}
