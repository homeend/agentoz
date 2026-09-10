package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type reply = struct {
	out string
	err error
}

// ggScript routes gg calls to a fake; everything else keeps the previous
// runner (wt). Returns the recorded gg args. Install AFTER swapWt so both
// chain.
func ggScript(t *testing.T, root string, replies map[string]reply) *[]string {
	t.Helper()
	var calls []string
	prev := testSrv.run
	testSrv.run = func(dir, name string, args ...string) ([]byte, error) {
		if name != "gg" {
			return prev(dir, name, args...)
		}
		if dir != root {
			t.Errorf("gg ran in %s, want %s", dir, root)
		}
		key := strings.Join(args, " ")
		calls = append(calls, key)
		r, ok := replies[key]
		if !ok {
			t.Errorf("unexpected gg %s", key)
			return nil, errors.New("unexpected")
		}
		return []byte(r.out), r.err
	}
	t.Cleanup(func() { testSrv.run = prev })
	return &calls
}

// swapWt makes `wt list` return the given JSON from now on.
func swapWt(t *testing.T, wtOut string) {
	t.Helper()
	prev := testSrv.run
	testSrv.run = func(dir, name string, args ...string) ([]byte, error) {
		if name == "wt" {
			return []byte(wtOut), nil
		}
		return prev(dir, name, args...)
	}
	t.Cleanup(func() { testSrv.run = prev })
}

func execNotFound() error { return &exec.Error{Name: "gg", Err: exec.ErrNotFound} }

func projectID(t *testing.T, tsURL, root string) (pid, mainCh int64) {
	t.Helper()
	resp := postJSON(t, tsURL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	pid = int64(p["id"].(float64))
	mainCh = int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	return
}

func TestWorktreeFormListsBranches(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, mainCh := projectID(t, ts.URL, root)
	ggScript(t, root, map[string]reply{
		"branch ls":     {out: "* main\n  feat/x\n  wt/feat ↑1\n"},
		"worktree list": {out: fmt.Sprintf("main\t%s\nwt/feat\t%s-wt-feat\n", root, root)},
	})
	body := getBody(t, fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid))
	for _, want := range []string{
		`<option value="feat/x">feat/x</option>`,
		fmt.Sprintf(`<option value="wt/feat" disabled>wt/feat (checked out in %s)</option>`, filepath.Base(root+"-wt-feat")),
		fmt.Sprintf(`<option value="main" disabled>main (checked out in %s)</option>`, filepath.Base(root)),
		`<option value="main" selected>main</option>`, // base select defaults to HEAD
		fmt.Sprintf(`href="/ui/channels/%d"`, mainCh),
		`name="mode" value="new" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

func TestWorktreeFormWhenGgMissing(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	ggScript(t, root, map[string]reply{"branch ls": {err: execNotFound()}})
	body := getBody(t, fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid))
	if !strings.Contains(body, `gg not found (gg_bin = &#34;gg&#34;)`) || strings.Contains(body, `name="mode"`) {
		t.Fatalf("expected the error without the form:\n%s", body)
	}
	if !strings.Contains(body, fmt.Sprintf(`action="/ui/projects/%d/shell"`, pid)) {
		t.Fatal("terminal button missing")
	}
}

func TestWorktreeCreateNewBranchRedirectsToChannel(t *testing.T) {
	ts, st, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	newPath := root + "-wt-hot"
	if err := os.MkdirAll(newPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// After creation the wt fixture must list the new worktree too.
	swapWt(t, fmt.Sprintf(`[{"path":%q,"branch":"refs/heads/main","head":"a","is_main":true},{"path":%q,"branch":"refs/heads/wt/feat","head":"b","is_main":false},{"path":%q,"branch":"refs/heads/hot","head":"c","is_main":false}]`, root, root+"-wt-feat", newPath))
	calls := ggScript(t, root, map[string]reply{
		"worktree add --from main hot": {out: "→ creating worktree: hot → " + newPath + "\n✓ created worktree hot at " + newPath + "\n"},
	})
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid),
		url.Values{"mode": {"new"}, "name": {"hot"}, "base": {"main"}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if len(*calls) != 1 {
		t.Fatalf("gg calls = %v", *calls)
	}
	chans, _ := st.ChannelsByProject(pid)
	var hot int64
	for _, c := range chans {
		if c.WorktreePath == newPath {
			hot = c.ID
		}
	}
	if hot == 0 {
		t.Fatalf("no channel for %s: %+v", newPath, chans)
	}
	if loc := r.Header.Get("Location"); r.StatusCode != http.StatusFound || loc != fmt.Sprintf("/ui/channels/%d", hot) {
		t.Fatalf("redirect = %d %s", r.StatusCode, loc)
	}
}

func TestWorktreeCreateExistingBranchUsesBranchFlag(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	calls := ggScript(t, root, map[string]reply{
		"worktree add --branch feat/x": {out: "error: create worktree: no local branch \"feat/x\"\n", err: errors.New("exit status 1")},
		"branch ls":                    {out: "* main\n"},
		"worktree list":                {out: fmt.Sprintf("main\t%s\n", root)},
	})
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid),
		url.Values{"mode": {"existing"}, "branch": {"feat/x"}})
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, r)
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", r.StatusCode)
	}
	if (*calls)[0] != "worktree add --branch feat/x" {
		t.Fatalf("calls = %v", *calls)
	}
	for _, want := range []string{`no local branch &#34;feat/x&#34;`, fmt.Sprintf(`action="/ui/projects/%d/shell"`, pid), `name="mode" value="existing" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("failure page missing %q", want)
		}
	}
}

func TestWorktreeValidation(t *testing.T) {
	ts, _, root := newTestServer(t)
	pid, _ := projectID(t, ts.URL, root)
	calls := ggScript(t, root, map[string]reply{"branch ls": {out: "* main\n"}, "worktree list": {out: "main\t" + root + "\n"}})
	for _, f := range []url.Values{
		{"mode": {"new"}, "name": {""}, "base": {"main"}},
		{"mode": {"new"}, "name": {"has space"}, "base": {"main"}},
		{"mode": {"new"}, "name": {"x"}, "base": {""}},
		{"mode": {"existing"}, "branch": {""}},
		{"mode": {"weird"}},
	} {
		r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/worktree", ts.URL, pid), f)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v: status %d", f, r.StatusCode)
		}
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "worktree add") {
			t.Fatalf("gg add ran on invalid input: %v", *calls)
		}
	}
}

func TestShellButtonSpawnsShellInMainChannel(t *testing.T) {
	ts, st, root := newTestServer(t)
	withShell(t)
	fs := &fakeSpawner{handle: "s:7"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	pid, mainCh := projectID(t, ts.URL, root)
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/shell", ts.URL, pid), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	runs, _ := st.RunsByChannel(mainCh)
	if len(runs) != 1 || runs[0].Provider != "shell" || runs[0].Workdir != root {
		t.Fatalf("runs = %+v", runs)
	}
	if loc := r.Header.Get("Location"); loc != fmt.Sprintf("/ui/runs/%d/terminal", runs[0].ID) {
		t.Fatalf("redirect = %s", loc)
	}
}
