package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"erbrus/internal/spawn"
)

// fakeViewer is a fakeSpawner whose windows can be viewed: the "tmux
// client" is a shell that prints its size and echoes.
type fakeViewer struct {
	*fakeSpawner
	cols, rows int
	sizeErr    error
	argv       []string
}

func (f *fakeViewer) ViewCommand(h spawn.Handle, view string) ([]string, error) {
	return f.argv, nil
}
func (f *fakeViewer) Size(h spawn.Handle) (int, int, error) { return f.cols, f.rows, f.sizeErr }

func wsURL(tsURL string, runID int64) string {
	return "ws" + strings.TrimPrefix(tsURL, "http") + fmt.Sprintf("/ui/runs/%d/terminal", runID)
}

// readUntilWS drains frames until want appears in binary data (or the
// context ends), returning everything seen.
func readUntilWS(ctx context.Context, c *websocket.Conn, want string) (string, error) {
	var sb strings.Builder
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return sb.String(), err
		}
		if typ == websocket.MessageBinary {
			sb.Write(data)
			if strings.Contains(sb.String(), want) {
				return sb.String(), nil
			}
		}
	}
}

func TestTerminalRelaysBytesAndSize(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	ts, st, root := newTestServer(t)
	fv := &fakeViewer{fakeSpawner: &fakeSpawner{}, cols: 100, rows: 30, argv: []string{"sh", "-c", "stty size; cat"}}
	testSrv.SetRuntime(fv, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts.URL, run.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	typ, data, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageText || string(data) != `{"cols":100,"rows":30}` {
		t.Fatalf("first frame = %v %q %v", typ, data, err)
	}
	if got, err := readUntilWS(ctx, c, "30 100"); err != nil {
		t.Fatalf("pty size not relayed: %q %v", got, err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("ping\r")); err != nil {
		t.Fatal(err)
	}
	if got, err := readUntilWS(ctx, c, "ping"); err != nil {
		t.Fatalf("input not relayed: %q %v", got, err)
	}
	c.Close(websocket.StatusNormalClosure, "")
	// The handler must let go of its slot once the socket is gone.
	deadline := time.Now().Add(3 * time.Second)
	for testSrv.openTerminals() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := testSrv.openTerminals(); n != 0 {
		t.Fatalf("terminals still open after close: %d", n)
	}
}

func TestTerminalRefusals(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	// Spawner without Viewer: 501 before any upgrade.
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/terminal", ts.URL, run.ID))
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("plain spawner status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Finished run: 404.
	fv := &fakeViewer{fakeSpawner: fs, cols: 80, rows: 24, argv: []string{"cat"}}
	testSrv.SetRuntime(fv, "/abs/erbrus", ts.URL)
	st.FinishRun(run.ID, "done", 0)
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/terminal", ts.URL, run.ID))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("finished run status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Window gone at connect time: upgrade, then close 1011 with the reason.
	run2 := claudeRun(t, st, ch1)
	fv.sizeErr = fmt.Errorf("can't find window")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts.URL, run2.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusInternalError {
		t.Fatalf("close status = %v (%v)", websocket.CloseStatus(err), err)
	}
	if !strings.Contains(err.Error(), "can't find window") {
		t.Fatalf("close reason lost: %v", err)
	}
}

func TestTerminalLimit(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	ts, st, root := newTestServer(t)
	fv := &fakeViewer{fakeSpawner: &fakeSpawner{}, cols: 80, rows: 24, argv: []string{"cat"}}
	testSrv.SetRuntime(fv, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	old := maxTerminals
	maxTerminals = 1
	defer func() { maxTerminals = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts.URL, run.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err := c.Read(ctx); err != nil { // size frame: the terminal is up
		t.Fatal(err)
	}
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/terminal", ts.URL, run.ID))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second terminal status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}
