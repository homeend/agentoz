package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/store"
)

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
}

// runningAgent seeds a running run with a tmux target in chID.
func runningAgent(t *testing.T, st *store.Store, chID int64, name string) store.AgentRun {
	t.Helper()
	run, err := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex",
		AgentName: name, Workdir: "/w", Status: "running", Spawner: "tmux", TmuxTarget: "s:5"})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestComposerMemoDefault(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"target": {"memo"}, "body": {"note to self"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	msgs, _ := st.MessagesSince(ch1, 0, 100)
	last := msgs[len(msgs)-1]
	if last.Body != "note to self" || last.Kind != "message" || last.TargetLabel != "" {
		t.Fatalf("memo stored wrong: %+v", last)
	}
	// One's own message never counts as unread.
	un, _ := st.UnreadByChannel()
	if _, ok := un[ch1]; ok {
		t.Fatalf("own memo left channel unread: %+v", un[ch1])
	}
}

func TestComposerSendToAgent(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"target": {fmt.Sprintf("r%d", run.ID)}, "body": {"fix the login bug"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(fs.sent) != 1 || !strings.HasPrefix(fs.sent[0], "s:5|fix the login bug") {
		t.Fatalf("sent = %q", fs.sent)
	}
	// Chat-delivered text carries reply routing — the agent can't otherwise
	// tell it came from the channel and not the keyboard.
	if !strings.Contains(fs.sent[0], `msg send --report`) {
		t.Fatalf("delivery missing reply instructions: %q", fs.sent[0])
	}
	msgs, _ := st.MessagesSince(ch1, 0, 100)
	last := msgs[len(msgs)-1]
	if last.TargetLabel != "claude" || last.Body != "fix the login bug" {
		t.Fatalf("agent message stored wrong: %+v", last)
	}
}

func TestComposerSendToDeadAgentWarns(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{sendErr: fmt.Errorf("no window")}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"target": {fmt.Sprintf("r%d", run.ID)}, "body": {"hello"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "warning=") {
		t.Fatalf("redirect should carry warning, got %q", loc)
	}
	// The message still lands in the channel.
	msgs, _ := st.MessagesSince(ch1, 0, 100)
	if msgs[len(msgs)-1].Body != "hello" {
		t.Fatal("message not stored despite send failure")
	}
}

func TestComposerRejectsForeignAgent(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	other := runningAgent(t, st, ch2, "codex")

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, ch1),
		url.Values{"target": {fmt.Sprintf("r%d", other.ID)}, "body": {"hi"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for agent from another channel", resp.StatusCode)
	}
}

func TestChannelPageComposerTargets(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")
	done, _ := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex",
		AgentName: "old", Workdir: "/w", Status: "done", Spawner: "tmux"})

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, `value="memo"`) {
		t.Fatal("composer missing memo option")
	}
	if !strings.Contains(html, fmt.Sprintf(`value="r%d"`, run.ID)) {
		t.Fatal("composer missing running agent option")
	}
	if strings.Contains(html, fmt.Sprintf(`value="r%d"`, done.ID)) {
		t.Fatal("finished run must not be a composer target")
	}
}

func TestForwardToAgentDeliversContext(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	target := runningAgent(t, st, ch2, "codex")
	src, err := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "report",
		AuthorKind: "agent", AuthorName: "claude", Body: "auth is finished"})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := noRedirect().PostForm(ts.URL+"/ui/forward", url.Values{
		"message_id": {fmt.Sprint(src.ID)}, "target": {fmt.Sprintf("r%d", target.ID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// Copy with provenance lands in the agent's channel.
	msgs, _ := st.MessagesSince(ch2, 0, 100)
	var copied bool
	for _, m := range msgs {
		if m.OriginMessageID == src.ID && m.Body == "auth is finished" {
			copied = true
		}
	}
	if !copied {
		t.Fatal("forwarded copy missing in agent's channel")
	}
	// Injection carries origin coordinates and reply routing.
	if len(fs.sent) != 1 {
		t.Fatalf("sent = %q", fs.sent)
	}
	for _, want := range []string{
		"s:5|",
		"forwarded from",
		"auth is finished",
		fmt.Sprintf("--channel %d", ch1),
	} {
		if !strings.Contains(fs.sent[0], want) {
			t.Fatalf("injected text missing %q:\n%s", want, fs.sent[0])
		}
	}
}

func TestForwardDialogListsAgents(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch2, "codex")
	src, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "memo text"})

	resp, err := http.Get(fmt.Sprintf("%s/ui/forward?message=%d", ts.URL, src.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, fmt.Sprintf(`value="r%d"`, run.ID)) {
		t.Fatal("forward dialog missing agent target")
	}
	if !strings.Contains(html, fmt.Sprintf(`value="c%d"`, ch2)) {
		t.Fatal("forward dialog missing channel target")
	}
}

func TestMemoIsForwardable(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	m, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "plain memo"})

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, fmt.Sprintf("/ui/forward?message=%d", m.ID)) {
		t.Fatal("memo has no Forward action in the channel view")
	}
}

func TestUIDeleteMessage(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	m, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "oops"})
	artDir := filepath.Join(testSrv.dataDir, "artifacts", fmt.Sprint(m.ID))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/messages/%d/delete", ts.URL, m.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, ok, _ := st.MessageByID(m.ID); ok {
		t.Fatal("message survived")
	}
	if _, err := os.Stat(artDir); !os.IsNotExist(err) {
		t.Fatal("artifact dir survived")
	}
}

func TestUIClearChannel(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	m, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", Body: "r"})
	if _, err := st.AddArtifact(store.Artifact{MessageID: m.ID, Filename: "f", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	artDir := filepath.Join(testSrv.dataDir, "artifacts", fmt.Sprint(m.ID))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/clear", ts.URL, ch1), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if msgs, _ := st.MessagesSince(ch1, 0, 100); len(msgs) != 0 {
		t.Fatalf("messages remain: %d", len(msgs))
	}
	if _, err := os.Stat(artDir); !os.IsNotExist(err) {
		t.Fatal("artifact dir survived")
	}
}

func TestChannelPageHasDeleteAndClearActions(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	m, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "hello"})

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, fmt.Sprintf("/ui/messages/%d/delete", m.ID)) {
		t.Fatal("message missing delete action")
	}
	if !strings.Contains(html, fmt.Sprintf("/ui/channels/%d/clear", ch1)) {
		t.Fatal("channel missing clear-chat action")
	}
}

func TestMarkdownMessageRenders(t *testing.T) {
	ts, _, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)

	resp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, ch1),
		map[string]string{"kind": "report", "format": "md",
			"body": "# Done\n\n**bold** and <script>alert(1)</script>"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	page, err := http.Get(fmt.Sprintf("%s/ui/channels/%d/stream", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	html := readBody(t, page)
	if !strings.Contains(html, "<h1>Done</h1>") || !strings.Contains(html, "<strong>bold</strong>") {
		t.Fatalf("markdown not rendered:\n%s", html)
	}
	if strings.Contains(html, "<script>") {
		t.Fatal("raw HTML must be stripped from markdown messages")
	}
}

func TestInvalidFormatRejected(t *testing.T) {
	ts, _, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	resp := postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, ch1),
		map[string]string{"body": "x", "format": "html"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMdArtifactRendersInline(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	m, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", Body: "see attached"})
	dir := filepath.Join(testSrv.dataDir, "artifacts", fmt.Sprint(m.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("## Findings\n\n- item one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddArtifact(store.Artifact{MessageID: m.ID, Filename: "notes.md", Path: path, Size: 24}); err != nil {
		t.Fatal(err)
	}

	page, err := http.Get(fmt.Sprintf("%s/ui/channels/%d/stream", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	html := readBody(t, page)
	if !strings.Contains(html, "<h2>Findings</h2>") || !strings.Contains(html, "<li>item one</li>") {
		t.Fatalf("md artifact not rendered inline:\n%s", html)
	}
	if !strings.Contains(html, "notes.md") {
		t.Fatal("download link must remain")
	}
}

func TestAgentReadExcludesSystemMessages(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")
	if _, err := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "system",
		AuthorKind: "system", Body: "claude stop hook: prompt finished"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", Body: "real content"}); err != nil {
		t.Fatal(err)
	}

	get := func(token string) []map[string]any {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, ch1), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return decode[[]map[string]any](t, resp)
	}

	for _, m := range get(run.Token) {
		if m["kind"] == "system" {
			t.Fatalf("agent read returned a system message: %v", m)
		}
	}
	var humanSeesSystem bool
	for _, m := range get("") {
		if m["kind"] == "system" {
			humanSeesSystem = true
		}
	}
	if !humanSeesSystem {
		t.Fatal("unauthenticated (human) read must still include system messages")
	}
}

func TestUIDeleteBatch(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	m1, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "a"})
	m2, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "system",
		AuthorKind: "system", Body: "b"})
	keep, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "keep"})
	foreign, _ := st.CreateMessage(store.Message{ChannelID: ch2, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "other channel"})

	resp, err := noRedirect().PostForm(fmt.Sprintf("%s/ui/channels/%d/messages/delete-batch", ts.URL, ch1),
		url.Values{"msg": {fmt.Sprint(m1.ID), fmt.Sprint(m2.ID), fmt.Sprint(foreign.ID)}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, id := range []int64{m1.ID, m2.ID} {
		if _, ok, _ := st.MessageByID(id); ok {
			t.Fatalf("message %d survived batch delete", id)
		}
	}
	if _, ok, _ := st.MessageByID(keep.ID); !ok {
		t.Fatal("unchecked message deleted")
	}
	if _, ok, _ := st.MessageByID(foreign.ID); !ok {
		t.Fatal("cross-channel id must be ignored, not deleted")
	}
}

func TestChannelPageHasBatchControls(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	m, _ := st.CreateMessage(store.Message{ChannelID: ch1, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "x"})

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	for _, want := range []string{
		`id="batchdel"`, `id="unselect-all"`, "Delete selected",
		fmt.Sprintf(`value="%d" form="batchdel"`, m.ID),
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("channel page missing %q", want)
		}
	}
}
