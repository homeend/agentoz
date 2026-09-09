package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"erbrus/internal/store"
)

// The projects page offers an × (with confirmation) only for archived
// channels that hold no messages.
func TestProjectsPageDeletesEmptyArchivedChannel(t *testing.T) {
	ts, st, root := newTestServer(t)
	_, ch2 := twoChannels(t, ts.URL, root)
	if err := st.SetChannelArchived(ch2, true); err != nil {
		t.Fatal(err)
	}
	form := fmt.Sprintf(`action="/ui/channels/%d/delete"`, ch2)

	resp, _ := http.Get(ts.URL + "/ui/projects")
	body := readAll(t, resp)
	if !strings.Contains(body, form) || !strings.Contains(body, `data-confirm="Delete the empty archived channel #`) {
		t.Fatalf("empty archived channel without a delete form:\n%s", body)
	}

	// One message and the offer is gone (the rail's delete stays for that).
	if _, err := st.CreateMessage(store.Message{ChannelID: ch2, Kind: "message", AuthorKind: "human", AuthorName: "you", Body: "kept"}); err != nil {
		t.Fatal(err)
	}
	resp, _ = http.Get(ts.URL + "/ui/projects")
	if body := readAll(t, resp); strings.Contains(body, form) {
		t.Fatalf("archived channel with messages must not offer deletion on the card:\n%s", body)
	}

	// The form's target deletes an archived channel and lands on the projects page.
	if err := st.DeleteMessage(func() int64 {
		msgs, _ := st.MessagesSince(ch2, 0, 10)
		return msgs[0].ID
	}()); err != nil {
		t.Fatal(err)
	}
	r, err := noRedirect().Post(fmt.Sprintf("%s/ui/channels/%d/delete", ts.URL, ch2), "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/ui/projects") {
		t.Fatalf("delete: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if _, ok, _ := st.ChannelByID(ch2); ok {
		t.Fatal("channel still exists")
	}
}
