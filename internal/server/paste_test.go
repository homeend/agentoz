package server

import (
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

func fastPaste(t *testing.T) {
	t.Helper()
	oldPoll, oldDead, oldSettle := pastePoll, pasteDeadline, pasteSettle
	pastePoll, pasteDeadline, pasteSettle = 5*time.Millisecond, 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { pastePoll, pasteDeadline, pasteSettle = oldPoll, oldDead, oldSettle })
}

func TestPasteModeDetection(t *testing.T) {
	if pasteMode(config.Provider{Command: `k "{prompt}"`}) || !pasteMode(config.Provider{Command: `k {args}`}) ||
		!pasteMode(config.Provider{Command: `k "{prompt}"`, Prompt: "paste"}) || pasteMode(config.Provider{Command: `k`, Prompt: "arg"}) {
		t.Fatal("pasteMode")
	}
}

// spawnPasteProvider spawns a "kimi" run whose command has no {prompt}.
func spawnPasteProvider(t *testing.T, rules bool) (*fakeSpawner, int64) {
	t.Helper()
	fastPaste(t)
	ts, _, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:5", alive: map[spawn.Handle]bool{"s:5": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	p := config.Provider{Command: `kimi --yolo {args}`}
	if rules {
		p.ScreenWaiting = []string{`^│ >[^\n]*\n╰`}
		p.ScreenQuestion = []string{`Trust this folder\?`}
	}
	if testSrv.cfg.Providers == nil {
		testSrv.cfg.Providers = map[string]config.Provider{}
	}
	testSrv.cfg.Providers["kimi"] = p
	testSrv.setRules("kimi", nil)
	if rules {
		r, err := screen.Compile(p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion)
		if err != nil {
			t.Fatal(err)
		}
		testSrv.setRules("kimi", &r)
	}
	ch1, _ := twoChannels(t, ts.URL, root)
	resp, out := doJSON(t, "POST", ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "kimi", "name": "kimi", "prompt": "do the thing"})
	if resp.StatusCode != 201 {
		t.Fatalf("spawn: %d %v", resp.StatusCode, out)
	}
	return fs, ch1
}

func waitSent(fs *fakeSpawner, d time.Duration) []string {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		fs.capMu.Lock()
		n := len(fs.sent)
		fs.capMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	fs.capMu.Lock()
	defer fs.capMu.Unlock()
	return append([]string(nil), fs.sent...)
}

func TestPasteDeliversOnWaiting(t *testing.T) {
	fs, ch1 := spawnPasteProvider(t, true)
	// The command line carries no prompt: the last argv element is the
	// wrapper's cmd.sh path, and the rendered command inside it has none.
	if strings.Contains(strings.Join(fs.specs[0].Command, " "), "do the thing") {
		t.Fatal("prompt must not be on the command line in paste mode")
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "Trust this folder?\n❯ Trust\n", Activity: time.Now()})
	if sent := waitSent(fs, 60*time.Millisecond); len(sent) != 0 {
		t.Fatal("must not paste over a dialog")
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "╭──╮\n│ >   │\n╰──╯\n", Activity: time.Now()})
	sent := waitSent(fs, 2*time.Second)
	if len(sent) != 1 || !strings.Contains(sent[0], "do the thing") || !strings.Contains(sent[0], "erbrus msg send") {
		t.Fatalf("sent = %v", sent)
	}
	time.Sleep(50 * time.Millisecond)
	if sent := waitSent(fs, 0); len(sent) != 1 {
		t.Fatal("pasted twice")
	}
	if got := systemBodies(t, testSrv.st, ch1); !strings.Contains(strings.Join(got, "\n"), "prompt delivered") {
		t.Fatalf("no delivered message: %v", got)
	}
}

func TestPasteWithoutRulesWaitsForStableScreen(t *testing.T) {
	fs, _ := spawnPasteProvider(t, false)
	fs.setScreen("s:5", spawn.Screen{Raw: "booting…\n", Activity: time.Now()})
	if sent := waitSent(fs, 2*time.Second); len(sent) != 1 {
		t.Fatalf("stable screen must trigger paste: %v", sent)
	}
}

func TestPasteTimeoutPostsNotDelivered(t *testing.T) {
	fs, ch1 := spawnPasteProvider(t, true)
	fs.setScreen("s:5", spawn.Screen{Raw: "✻ Cogitating… (1s · x)\n", Activity: time.Now()}) // never waiting
	deadline := time.Now().Add(pasteDeadline + 2*time.Second)
	for time.Now().Before(deadline) {
		if got := systemBodies(t, testSrv.st, ch1); strings.Contains(strings.Join(got, "\n"), "NOT delivered") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sent := waitSent(fs, 0); len(sent) != 0 {
		t.Fatal("must not paste while working")
	}
	if got := systemBodies(t, testSrv.st, ch1); !strings.Contains(strings.Join(got, "\n"), "NOT delivered") {
		t.Fatalf("no timeout message: %v", got)
	}
}

func TestArgModeNeverPastes(t *testing.T) {
	fastPaste(t)
	ts, _, root := newTestServer(t)
	fs := &fakeSpawner{handle: "s:5", alive: map[spawn.Handle]bool{"s:5": true}}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	ch1, _ := twoChannels(t, ts.URL, root)
	resp, out := doJSON(t, "POST", ts.URL+"/api/runs", map[string]any{"channel_id": ch1, "provider": "codex", "name": "c", "prompt": "hi"})
	if resp.StatusCode != 201 {
		t.Fatalf("spawn: %d %v", resp.StatusCode, out)
	}
	fs.setScreen("s:5", spawn.Screen{Raw: "──────────\n❯\n", Activity: time.Now()})
	if sent := waitSent(fs, 100*time.Millisecond); len(sent) != 0 {
		t.Fatalf("arg mode pasted: %v", sent)
	}
}
