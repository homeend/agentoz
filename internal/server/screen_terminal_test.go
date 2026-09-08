package server

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"erbrus/internal/spawn"
)

func TestScreenEventsWithoutHTML(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	fs.setScreen("s:5", spawn.Screen{Raw: "hello\n" + box, Activity: time.Now()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/ui/runs/%d/screen/events?html=0", ts.URL, run.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		if strings.Contains(line, `"html"`) {
			t.Fatalf("html present with ?html=0: %s", line)
		}
		if !strings.Contains(line, `"state":"waiting"`) {
			t.Fatalf("state missing: %s", line)
		}
		return
	}
	t.Fatal("no frame received")
}

func TestScreenPageTerminalFlag(t *testing.T) {
	ts, st, root := newTestServer(t)
	fs := &fakeSpawner{}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	run := claudeRun(t, st, ch1)
	fs.setScreen("s:5", spawn.Screen{Raw: box, Activity: time.Now()})

	// Plain spawner: no terminal, the live <pre> as before.
	resp, _ := http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	body := readAll(t, resp)
	if strings.Contains(body, `id="term"`) || strings.Contains(body, "vendor/xterm.js") || !strings.Contains(body, `<pre class="screen" id="screen"`) {
		t.Fatalf("plain spawner page: %s", body)
	}
	// Viewer spawner: terminal container, vendored assets, hidden <pre>.
	testSrv.SetRuntime(&fakeViewer{fakeSpawner: fs, cols: 80, rows: 24, argv: []string{"cat"}}, "/abs/erbrus", ts.URL)
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	body = readAll(t, resp)
	for _, want := range []string{
		fmt.Sprintf(`id="term" data-ws="/ui/runs/%d/terminal"`, run.ID),
		`/static/vendor/xterm.js?v=`, `/static/vendor/xterm.css?v=`,
		`id="screen" data-run="` + fmt.Sprint(run.ID) + `" hidden`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("viewer page missing %q", want)
		}
	}
	if strings.Contains(body, "use the channel composer") {
		t.Error("composer hint must go when the terminal is present")
	}
	// Finished run: no terminal even with a Viewer.
	st.FinishRun(run.ID, "done", 0)
	resp, _ = http.Get(fmt.Sprintf("%s/ui/runs/%d/screen", ts.URL, run.ID))
	if body := readAll(t, resp); strings.Contains(body, `id="term"`) {
		t.Error("finished run must not offer a terminal")
	}
}
