package spawn

import (
	"errors"
	"strings"
	"testing"
)

// recorder captures every command and replays scripted responses.
type recorder struct {
	calls []string
	// responses keyed by command prefix ("tmux has-session"), value: output or error
	fail map[string]error
	out  map[string]string
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	for prefix, err := range r.fail {
		if strings.HasPrefix(call, prefix) {
			return nil, err
		}
	}
	for prefix, out := range r.out {
		if strings.HasPrefix(call, prefix) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

func spec() RunSpec {
	return RunSpec{
		Session:    "erbrus-webshop",
		WindowName: "review-kimi",
		Workdir:    "/code/webshop",
		Env:        map[string]string{"ERBRUS_URL": "http://127.0.0.1:7420", "ERBRUS_CHANNEL": "3"},
		Command:    []string{"/abs/erbrus", "wrap", "/data/runs/7/cmd.sh"},
	}
}

func TestSpawnManagedCreatesSession(t *testing.T) {
	rec := &recorder{
		fail: map[string]error{"tmux has-session": errors.New("no session")},
		out:  map[string]string{"tmux new-window": "erbrus-webshop:1\n"},
	}
	h, err := NewTmux(rec.run).Spawn(spec())
	if err != nil {
		t.Fatal(err)
	}
	if h != "erbrus-webshop:1" {
		t.Errorf("handle = %q", h)
	}
	want := []string{
		"tmux has-session -t =erbrus-webshop",
		"tmux new-session -d -s erbrus-webshop -c /code/webshop",
		"tmux new-window -d -P -F #{session_name}:#{window_index} -t =erbrus-webshop: -n review-kimi -c /code/webshop -e ERBRUS_CHANNEL=3 -e ERBRUS_URL=http://127.0.0.1:7420 -- /abs/erbrus wrap /data/runs/7/cmd.sh",
		"tmux set-option -t =erbrus-webshop:1 remain-on-exit on",
	}
	if len(rec.calls) != len(want) {
		t.Fatalf("calls = %v", rec.calls)
	}
	for i := range want {
		if rec.calls[i] != want[i] {
			t.Errorf("call %d:\n got  %q\n want %q", i, rec.calls[i], want[i])
		}
	}
}

func TestSpawnManagedReusesSession(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux new-window": "erbrus-webshop:4\n"}}
	if _, err := NewTmux(rec.run).Spawn(spec()); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "tmux new-session") {
			t.Error("must not create a session that exists")
		}
	}
}

func TestSpawnAttachMode(t *testing.T) {
	s := spec()
	s.AttachSession = "main"
	rec := &recorder{out: map[string]string{"tmux new-window": "main:9\n"}}
	h, err := NewTmux(rec.run).Spawn(s)
	if err != nil {
		t.Fatal(err)
	}
	if h != "main:9" {
		t.Errorf("handle = %q", h)
	}
	if !strings.Contains(rec.calls[1], "-t =main:") {
		t.Errorf("window not opened in attach session: %v", rec.calls)
	}
}

func TestSpawnAttachMissingSessionFails(t *testing.T) {
	s := spec()
	s.AttachSession = "gone"
	rec := &recorder{fail: map[string]error{"tmux has-session": errors.New("no session")}}
	if _, err := NewTmux(rec.run).Spawn(s); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("want attach failure naming the session, got %v", err)
	}
	for _, c := range rec.calls {
		if strings.HasPrefix(c, "tmux new-session") {
			t.Error("attach mode must never create sessions")
		}
	}
}

func TestStopKillsWindow(t *testing.T) {
	rec := &recorder{}
	if err := NewTmux(rec.run).Stop("erbrus-webshop:3"); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0] != "tmux kill-window -t =erbrus-webshop:3" {
		t.Errorf("calls = %v", rec.calls)
	}
}

func TestAlive(t *testing.T) {
	// Window 1 hosts a live pane, window 3's process died under
	// remain-on-exit (pane_dead=1): the window exists but the agent is gone.
	rec := &recorder{out: map[string]string{"tmux list-panes": "erbrus-webshop:1 0\nerbrus-webshop:3 1\n"}}
	tm := NewTmux(rec.run)
	if ok, _ := tm.Alive("erbrus-webshop:1"); !ok {
		t.Error("want alive")
	}
	if ok, _ := tm.Alive("erbrus-webshop:3"); ok {
		t.Error("dead pane must count as not alive")
	}
	if ok, _ := tm.Alive("erbrus-webshop:9"); ok {
		t.Error("missing window must count as not alive")
	}
	rec2 := &recorder{fail: map[string]error{"tmux list-panes": errors.New("no session")}}
	ok, err := NewTmux(rec2.run).Alive("gone:1")
	if err != nil || ok {
		t.Errorf("gone session => (false, nil), got (%v, %v)", ok, err)
	}
}

func TestSendKeys(t *testing.T) {
	rec := &recorder{}
	tm := NewTmux(rec.run)
	if err := tm.Send(Handle("erbrus-x:3"), "fix the tests"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux set-buffer -- fix the tests",
		"tmux paste-buffer -dp -t =erbrus-x:3",
		"tmux send-keys -t =erbrus-x:3 Enter",
	}
	if len(rec.calls) != 3 || rec.calls[0] != want[0] || rec.calls[1] != want[1] || rec.calls[2] != want[2] {
		t.Fatalf("calls = %q, want %q", rec.calls, want)
	}
}

func TestSendKeysError(t *testing.T) {
	rec := &recorder{fail: map[string]error{"tmux paste-buffer": errors.New("no window")}}
	tm := NewTmux(rec.run)
	if err := tm.Send(Handle("erbrus-x:3"), "hi"); err == nil {
		t.Fatal("want error when window is gone")
	}
}

// Every tmux target must use "=" exact-match syntax: without it tmux
// prefix-matches, and a global session "erbrus" silently resolves to an
// existing "erbrus-<project>" session.
func TestAllTargetsUseExactMatch(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux new-window": "erbrus:1\n"}}
	tm := NewTmux(rec.run)
	s := spec()
	s.Session = "erbrus"
	if _, err := tm.Spawn(s); err != nil {
		t.Fatal(err)
	}
	_ = tm.Send("erbrus:1", "hi")
	_ = tm.Stop("erbrus:1")
	_, _ = tm.Alive("erbrus:1")
	for _, c := range rec.calls {
		if i := strings.Index(c, "-t "); i >= 0 && !strings.HasPrefix(c[i+3:], "=") {
			t.Errorf("target without exact-match prefix: %q", c)
		}
	}
}

func TestCaptureIssuesExactTargetsAndParses(t *testing.T) {
	rec := &recorder{out: map[string]string{
		"tmux capture-pane": "\x1b[1mhello\x1b[0m\nworld\n",
		"tmux display":      "0 1788820410 134 36\n",
	}}
	sc, err := NewTmux(rec.run).Capture("erbrus-webshop:1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux capture-pane -p -e -t =erbrus-webshop:1",
		"tmux display -p -t =erbrus-webshop:1 #{pane_dead} #{window_activity} #{pane_width} #{pane_height}",
	}
	if strings.Join(rec.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(rec.calls, "\n"), strings.Join(want, "\n"))
	}
	if sc.Raw != "\x1b[1mhello\x1b[0m\nworld\n" {
		t.Errorf("raw = %q", sc.Raw)
	}
	if sc.Dead {
		t.Error("pane reported dead")
	}
	if sc.Activity.Unix() != 1788820410 {
		t.Errorf("activity = %v", sc.Activity)
	}
	if sc.Cols != 134 || sc.Rows != 36 {
		t.Errorf("size = %dx%d", sc.Cols, sc.Rows)
	}
}

func TestCaptureDeadPane(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux display": "1 0 80 24\n"}}
	sc, err := NewTmux(rec.run).Capture("s:2")
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Dead || !sc.Activity.IsZero() {
		t.Errorf("dead=%v activity=%v", sc.Dead, sc.Activity)
	}
}

func TestCaptureWindowGone(t *testing.T) {
	rec := &recorder{fail: map[string]error{"tmux capture-pane": errors.New("can't find window")}}
	if _, err := NewTmux(rec.run).Capture("s:9"); err == nil {
		t.Fatal("expected error when the window is gone")
	}
}
