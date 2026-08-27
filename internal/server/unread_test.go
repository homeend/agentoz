package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"erbrus/internal/store"
)

// twoChannels adds the test project and returns the IDs of its two
// channels (general + the worktree channel).
func twoChannels(t *testing.T, tsURL, root string) (ch1, ch2 int64) {
	t.Helper()
	resp := postJSON(t, tsURL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	if len(chans) < 2 {
		t.Fatalf("want 2 channels, got %d", len(chans))
	}
	ch1 = int64(chans[0].(map[string]any)["id"].(float64))
	ch2 = int64(chans[1].(map[string]any)["id"].(float64))
	return ch1, ch2
}

func post(t *testing.T, st *store.Store, chID int64, kind string) {
	t.Helper()
	if _, err := st.CreateMessage(store.Message{ChannelID: chID, Kind: kind,
		AuthorKind: "agent", AuthorName: "a", Body: "hello"}); err != nil {
		t.Fatal(err)
	}
}

func TestChannelPageMarksRead(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	post(t, st, ch1, "report")

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	un, err := st.UnreadByChannel()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := un[ch1]; ok {
		t.Fatalf("channel still unread after page view: %+v", un[ch1])
	}
}

func TestStreamFetchMarksRead(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, _ := twoChannels(t, ts.URL, root)
	post(t, st, ch1, "message")

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d/stream", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	un, _ := st.UnreadByChannel()
	if _, ok := un[ch1]; ok {
		t.Fatalf("channel still unread after stream fetch: %+v", un[ch1])
	}
}

func TestSidebarUnreadBadge(t *testing.T) {
	ts, st, root := newTestServer(t)
	ch1, ch2 := twoChannels(t, ts.URL, root)
	post(t, st, ch2, "message")
	post(t, st, ch2, "report")

	resp, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, ch1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	want := fmt.Sprintf(`data-unread="%d"`, ch2)
	if !strings.Contains(html, want) {
		t.Fatalf("sidebar missing badge for channel %d:\n%s", ch2, html)
	}
	if !strings.Contains(html, "unreadb attn") {
		t.Fatal("report unread should carry the attn class")
	}
	if !strings.Contains(html, ">2<") {
		t.Fatal("badge should show count 2")
	}
}

func TestProjectsPageUnreadBadge(t *testing.T) {
	ts, st, root := newTestServer(t)
	_, ch2 := twoChannels(t, ts.URL, root)
	post(t, st, ch2, "message")

	resp, err := http.Get(ts.URL + "/ui/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html := readBody(t, resp)
	if !strings.Contains(html, "unreadb") || !strings.Contains(html, ">1<") {
		t.Fatalf("projects page missing unread badge:\n%s", html)
	}
}
