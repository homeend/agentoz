package server

import (
	"fmt"
	"net/http"
	"net/url"
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
