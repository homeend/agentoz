package server

import (
	"erbrus/internal/spawn"
	"erbrus/internal/store"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture lists two worktrees: root and root-wt-feat. Adding a project
// syncs too, so the worktree dir must exist at add time; removing it
// afterwards is the "worktree removed" situation.
func TestSyncArchivesRestoresCreates(t *testing.T) {
	ts, st, root := newTestServer(t)
	if err := os.MkdirAll(root+"-wt-feat", 0o755); err != nil {
		t.Fatal(err)
	}
	ch1, ch2 := twoChannels(t, ts.URL, root)
	c1, _, _ := st.ChannelByID(ch1)
	pid := c1.ProjectID
	os.RemoveAll(root + "-wt-feat")

	// Before any sync: the channel is live but flagged.
	page, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch2))
	if body := readAll(t, page); !strings.Contains(body, "path missing") || strings.Contains(body, "This channel is archived") {
		t.Fatalf("pre-sync channel page: %s", body)
	}

	// Sync: feat is archived, general untouched.
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/sync", ts.URL, pid), url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "archived") {
		t.Fatalf("sync redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if c, _, _ := st.ChannelByID(ch2); !c.Archived {
		t.Fatal("feat channel not archived")
	}
	if c, _, _ := st.ChannelByID(ch1); c.Archived {
		t.Fatal("general must never be archived")
	}
	// Hidden from the sidebar and spawn targets, listed as archived on the card.
	page, _ = http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if body := readAll(t, page); strings.Contains(body, fmt.Sprintf(`href="/ui/channels/%d"`, ch2)) {
		t.Fatal("archived channel still in sidebar")
	}
	sp, _ := http.Get(fmt.Sprintf("%s/ui/spawn?channel=%d", ts.URL, ch1))
	if body := readAll(t, sp); strings.Contains(body, fmt.Sprintf(`<option value="%d"`, ch2)) {
		t.Fatal("archived channel offered as spawn target")
	}
	pp, _ := http.Get(ts.URL + "/ui/projects")
	if body := readAll(t, pp); !strings.Contains(body, `class="chan archived"`) {
		t.Fatalf("projects page lacks archived list: %s", body)
	}
	page, _ = http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch2))
	if body := readAll(t, page); !strings.Contains(body, "This channel is archived") || strings.Contains(body, "+ Spawn agent") {
		t.Fatalf("archived channel page: banner missing or spawn offered: %s", body)
	}

	// The worktree comes back: restored. A new worktree appears: created.
	if err := os.MkdirAll(root+"-wt-feat", 0o755); err != nil {
		t.Fatal(err)
	}
	newDir := root + "-wt-hotfix"
	os.MkdirAll(newDir, 0o755)
	three := fmt.Sprintf(`[{"path": %q, "branch": "refs/heads/main", "head": "a", "is_main": true},
		{"path": %q, "branch": "refs/heads/wt/feat", "head": "b", "is_main": false},
		{"path": %q, "branch": "refs/heads/hotfix", "head": "c", "is_main": false},
		{"path": %q, "branch": "refs/heads/ghost", "head": "d", "is_main": false}]`, root, root+"-wt-feat", newDir, root+"-wt-ghost")
	testSrv.run = func(dir, name string, args ...string) ([]byte, error) { return []byte(three), nil }
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/sync", ts.URL, pid), url.Values{})
	r.Body.Close()
	loc := r.Header.Get("Location")
	if !strings.Contains(loc, "restored") || !strings.Contains(loc, "created") {
		t.Fatalf("second sync redirect: %s", loc)
	}
	if c, _, _ := st.ChannelByID(ch2); c.Archived {
		t.Fatal("feat channel not restored")
	}
	if _, ok, _ := st.ChannelByName(pid, "hotfix"); !ok {
		t.Fatal("hotfix channel not created")
	}
	// Listed by git but no directory: created straight into the archive.
	if g, ok, _ := st.ChannelByName(pid, "ghost"); !ok || !g.Archived {
		t.Fatalf("ghost worktree channel: ok=%v %+v", ok, g)
	}

	// Sync can redirect back to a channel.
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/sync", ts.URL, pid), url.Values{"channel": {fmt.Sprint(ch2)}})
	r.Body.Close()
	if loc := r.Header.Get("Location"); !strings.HasPrefix(loc, fmt.Sprintf("/ui/channels/%d", ch2)) {
		t.Fatalf("channel redirect: %s", loc)
	}
}

// An unmounted drive makes every path vanish at once; that must not
// archive anything.
func TestSyncSkipsArchiveWhenRepoMissing(t *testing.T) {
	ts, st, root := newTestServer(t)
	if err := os.MkdirAll(root+"-wt-feat", 0o755); err != nil {
		t.Fatal(err)
	}
	ch1, ch2 := twoChannels(t, ts.URL, root)
	c1, _, _ := st.ChannelByID(ch1)
	os.RemoveAll(root + "-wt-feat")
	os.RemoveAll(root)
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/sync", ts.URL, c1.ProjectID), url.Values{})
	r.Body.Close()
	if c, _, _ := st.ChannelByID(ch2); c.Archived {
		t.Fatal("archived while the repo root itself is missing")
	}
}

func TestDeleteArchivedChannelOnly(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	c1, _, _ := st.ChannelByID(ch1)
	pid := c1.ProjectID
	if _, err := st.CreateMessage(store.Message{ChannelID: ch2, Kind: "message", AuthorKind: "human", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	run := runningAgent(t, st, ch2, "ghost")
	runDir := filepath.Join(testSrv.dataDir, "runs", fmt.Sprint(run.ID))
	os.MkdirAll(runDir, 0o755)

	// Live channel: no button, delete refused.
	page, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch2))
	if body := readAll(t, page); strings.Contains(body, "Delete channel") {
		t.Fatal("delete offered on a live channel")
	}
	r, _ := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/delete", ts.URL, ch2), url.Values{})
	r.Body.Close()
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("live delete status = %d", r.StatusCode)
	}

	// Archive it (worktree gone), then the rail offers deletion.
	os.RemoveAll(root + "-wt-feat")
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/projects/%d/sync", ts.URL, pid), url.Values{})
	r.Body.Close()
	page, _ = http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch2))
	if body := readAll(t, page); !strings.Contains(body, fmt.Sprintf(`action="/ui/channels/%d/delete"`, ch2)) {
		t.Fatalf("archived channel lacks delete button: %s", body)
	}
	r, _ = noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/delete", ts.URL, ch2), url.Values{})
	r.Body.Close()
	if r.StatusCode != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/ui/projects?warning=deleted") {
		t.Fatalf("delete redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if _, ok, _ := st.ChannelByID(ch2); ok {
		t.Fatal("channel survived")
	}
	if _, ok, _ := st.RunByID(run.ID); ok {
		t.Fatal("run survived")
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatal("run dir survived")
	}
	if len(fs.killed) != 1 || fs.killed[0] != spawn.Handle("s:5") {
		t.Fatalf("running agent not stopped: %v", fs.killed)
	}
	if _, ok, _ := st.ChannelByID(ch1); !ok {
		t.Fatal("general must survive")
	}
}
