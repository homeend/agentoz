package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/config"
)

type fakeLauncher struct {
	calls []string // "dir|argv joined by a space"
	err   error
}

func (f *fakeLauncher) Start(dir string, argv []string) error {
	f.calls = append(f.calls, dir+"|"+strings.Join(argv, " "))
	return f.err
}

func withTools(t *testing.T, extra map[string]config.Tool) {
	t.Helper()
	testSrv.rulesMu.Lock()
	testSrv.cfg.Tools = config.BuiltinTools()
	for k, v := range extra {
		testSrv.cfg.Tools[k] = v
	}
	testSrv.rulesMu.Unlock()
}

func TestToolsRailListsToolsSorted(t *testing.T) {
	ts, _, root := newTestServer(t)
	withTools(t, map[string]config.Tool{"idea": {Command: "idea64.exe {windir}"}})
	ch1, _ := twoChannels(t, ts.URL, root)
	body := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	i1, i2, i3 := strings.Index(body, `tools/gg"`), strings.Index(body, `tools/idea"`), strings.Index(body, `tools/shell"`)
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Fatalf("tools rail wrong/missing: gg=%d idea=%d shell=%d", i1, i2, i3)
	}
	if !strings.Contains(body, `data-rail="tools"`) || !strings.Contains(body, fmt.Sprintf(`target="tool:gg:%d"`, ch1)) {
		t.Fatal("terminal tool must open in its own tab")
	}
	if strings.Contains(body, `target="tool:idea:`) {
		t.Fatal("GUI tool must not open a tab")
	}
}

func TestTerminalToolSpawnsToolRunInChannelDir(t *testing.T) {
	ts, st, root := newTestServer(t)
	withTools(t, nil)
	testSrv.rulesMu.Lock()
	testSrv.cfg.GgBin = "/opt/gg"
	testSrv.rulesMu.Unlock()
	fs := &fakeSpawner{handle: "s:3"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	_, ch2 := twoChannels(t, ts.URL, root) // the worktree channel: dir root+"-wt-feat"
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/gg", ts.URL, ch2), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	runs, _ := st.RunsByChannel(ch2)
	if len(runs) != 1 || runs[0].Provider != "tool:gg" || runs[0].AgentName != "gg" || runs[0].Workdir != root+"-wt-feat" || runs[0].Prompt != "" {
		t.Fatalf("run = %+v", runs)
	}
	if loc := r.Header.Get("Location"); r.StatusCode != http.StatusFound || loc != fmt.Sprintf("/ui/runs/%d/terminal", runs[0].ID) {
		t.Fatalf("redirect = %d %s", r.StatusCode, loc)
	}
	if fs.specs[0].Workdir != root+"-wt-feat" || fs.specs[0].WindowName != filepath.Base(root+"-wt-feat")+"/gg" {
		t.Fatalf("spec = %+v", fs.specs[0])
	}
	cmd := readCmdSh(t, testSrv.dataDir, runs[0].ID)
	if !strings.Contains(cmd, "exec /opt/gg\n") || strings.Contains(cmd, "erbrus msg") {
		t.Fatalf("cmd.sh = %q", cmd)
	}
	// It is a tool run: badge, no composer entry, no delivery.
	page := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch2))
	if !strings.Contains(page, `class="badge tool"`) || strings.Contains(page, fmt.Sprintf(`<option value="r%d">`, runs[0].ID)) {
		t.Fatal("tool run not rendered as a tool")
	}
	if w, _ := testSrv.sendToRun(runs[0], "x"); !strings.Contains(w, "not an agent") {
		t.Fatalf("sendToRun = %q", w)
	}
}

func TestTerminalToolWithoutSpawner(t *testing.T) {
	ts, _, root := newTestServer(t)
	withTools(t, nil)
	testSrv.SetRuntime(nil, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/shell", ts.URL, ch1), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if loc := r.Header.Get("Location"); r.StatusCode != http.StatusFound || !strings.Contains(loc, url.QueryEscape("need tmux")) {
		t.Fatalf("redirect = %d %s", r.StatusCode, loc)
	}
}

func TestGUIToolLaunchesDetachedAndReportsOnlyFailure(t *testing.T) {
	ts, st, root := newTestServer(t)
	withTools(t, map[string]config.Tool{"idea": {Command: "idea64.exe {windir} {dir}"}, "bad": {Command: "x {nope}"}})
	fl := &fakeLauncher{}
	testSrv.SetLauncher(fl)
	ch1, _ := twoChannels(t, ts.URL, root)
	before, _ := st.MessagesSince(ch1, 0, 100)

	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/idea", ts.URL, ch1), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if loc := r.Header.Get("Location"); loc != fmt.Sprintf("/ui/channels/%d", ch1) {
		t.Fatalf("success must redirect back silently, got %s", loc)
	}
	if len(fl.calls) != 1 || !strings.HasPrefix(fl.calls[0], root+"|idea64.exe ") || !strings.HasSuffix(fl.calls[0], " "+root) {
		t.Fatalf("launch = %v", fl.calls)
	}
	after, _ := st.MessagesSince(ch1, 0, 100)
	if len(after) != len(before) {
		t.Fatal("GUI launch must not post a note")
	}
	if runs, _ := st.RunsByChannel(ch1); len(runs) != 0 {
		t.Fatal("GUI launch must not create a run")
	}

	fl.err = errors.New(`exec: "idea64.exe": executable file not found`)
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/idea", ts.URL, ch1), nil)
	r.Body.Close()
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "warning=idea") || !strings.Contains(loc, url.QueryEscape("not found")) {
		t.Fatalf("failure redirect = %s", loc)
	}

	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/bad", ts.URL, ch1), nil)
	r.Body.Close()
	if loc := r.Header.Get("Location"); !strings.Contains(loc, url.QueryEscape("unknown placeholder {nope}")) {
		t.Fatalf("placeholder error not shown: %s", loc)
	}
	if len(fl.calls) != 2 {
		t.Fatalf("bad template must not launch: %v", fl.calls)
	}

	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/nope", ts.URL, ch1), nil)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown tool = %d", r.StatusCode)
	}
}

func TestArchivedChannelHasNoTools(t *testing.T) {
	ts, st, root := newTestServer(t)
	withTools(t, nil)
	_, ch2 := twoChannels(t, ts.URL, root)
	if err := st.SetChannelArchived(ch2, true); err != nil {
		t.Fatal(err)
	}
	if body := getBody(t, fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch2)); strings.Contains(body, `data-rail="tools"`) {
		t.Fatal("archived channel must not offer tools")
	}
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/tools/shell", ts.URL, ch2), nil)
	r.Body.Close()
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("archived POST = %d", r.StatusCode)
	}
}
