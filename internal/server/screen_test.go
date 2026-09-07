package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

func fastPoll(t *testing.T) {
	t.Helper()
	old := screenPollInterval
	screenPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { screenPollInterval = old })
}

func recvFrame(t *testing.T, ch <-chan screenFrame) screenFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return screenFrame{}
	}
}

func TestScreenFeedSendsOnChangeOnly(t *testing.T) {
	fastPoll(t)
	var mu sync.Mutex
	raw, calls := "one", 0
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return spawn.Screen{Raw: raw, Cols: 80, Rows: 24}, nil
	})
	ch, cancel := feed.Subscribe(7, "s:5", screen.Rules{})
	if f := recvFrame(t, ch); !strings.Contains(string(f.HTML), "one") || f.Cols != 80 {
		t.Fatalf("first frame = %+v", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("frame without change: %+v", f)
	case <-time.After(40 * time.Millisecond):
	}
	mu.Lock()
	raw = "two"
	mu.Unlock()
	if f := recvFrame(t, ch); !strings.Contains(string(f.HTML), "two") {
		t.Fatalf("changed frame = %+v", f)
	}
	cancel()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != n {
		t.Fatalf("still polling after last unsubscribe: %d -> %d", n, calls)
	}
}

func TestScreenFeedLateSubscriberGetsCurrentFrame(t *testing.T) {
	fastPoll(t)
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		return spawn.Screen{Raw: "steady"}, nil
	})
	ch1, cancel1 := feed.Subscribe(7, "s:5", screen.Rules{})
	defer cancel1()
	recvFrame(t, ch1)
	ch2, cancel2 := feed.Subscribe(7, "s:5", screen.Rules{})
	defer cancel2()
	if f := recvFrame(t, ch2); !strings.Contains(string(f.HTML), "steady") {
		t.Fatalf("late frame = %+v", f)
	}
}

func TestScreenFeedReportsCaptureError(t *testing.T) {
	fastPoll(t)
	feed := newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		return spawn.Screen{}, errTest("can't find window")
	})
	ch, cancel := feed.Subscribe(7, "s:5", screen.Rules{})
	defer cancel()
	f := recvFrame(t, ch)
	if f.Error != "screen unavailable: can't find window" {
		t.Fatalf("error frame = %+v", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("repeated error frame: %+v", f)
	case <-time.After(40 * time.Millisecond):
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestScreenPageRendersCapture(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	fs.setScreen("s:5", spawn.Screen{Raw: "\x1b[1mhello\x1b[0m <b>", Activity: time.Unix(1788820410, 0)})
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := runningAgent(t, st, ch1, "claude")

	resp, err := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		fmt.Sprintf(`id="screen" data-run="%d"`, run.ID),
		`<span style="font-weight:bold">hello</span> &lt;b&gt;`,
		`data-activity="1788820410"`,
		"claude", "s:5",
		fmt.Sprintf(`/ui/channels/%d`, ch1),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The runs panel links to the page.
	presp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, ch1))
	if pb := readAll(t, presp); !strings.Contains(pb, fmt.Sprintf(`href="/ui/runs/%d/screen"`, run.ID)) {
		t.Errorf("runs panel lacks Screen link: %s", pb)
	}
}

func TestScreenPageNoWindow(t *testing.T) {
	ts, st, root := newTestServer(t)
	testSrv.SetRuntime(&fakeSpawner{}, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run, err := st.CreateRun(store.AgentRun{ChannelID: ch1, Provider: "codex", AgentName: "fg",
		Workdir: "/w", Status: "running", Spawner: "fg"})
	if err != nil {
		t.Fatal(err)
	}
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(body, "no tmux window for this run") {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	eresp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen/events", ts.URL, run.ID))
	eresp.Body.Close()
	if eresp.StatusCode != http.StatusNotFound {
		t.Fatalf("events status = %d", eresp.StatusCode)
	}
	if r404, _ := http.Get(ts.URL + "/ui/runs/999999/screen"); r404.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown run status = %d", r404.StatusCode)
	}
}

func TestScreenEventsStreamFrames(t *testing.T) {
	fastPoll(t)
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (27s · x)\n❯\n"})
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/ui/runs/%d/screen/events", ts.URL, run.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	nextFrame := func() string {
		for sc.Scan() {
			if sc.Text() == "event: frame" && sc.Scan() {
				return strings.TrimPrefix(sc.Text(), "data: ")
			}
		}
		t.Fatalf("stream ended: %v", sc.Err())
		return ""
	}
	if d := nextFrame(); !strings.Contains(d, "Cogitating") || !strings.Contains(d, `"state":"working"`) || !strings.Contains(d, `"step":27`) {
		t.Fatalf("first frame = %s", d)
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "second", Dead: true})
	if d := nextFrame(); !strings.Contains(d, `"html":"second"`) || !strings.Contains(d, `"dead":true`) {
		t.Fatalf("second frame = %s", d)
	}
}
