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
		"tmux has-session -t erbrus-webshop",
		"tmux new-session -d -s erbrus-webshop -c /code/webshop",
		"tmux new-window -d -P -F #{session_name}:#{window_index} -t erbrus-webshop: -n review-kimi -c /code/webshop -e ERBRUS_CHANNEL=3 -e ERBRUS_URL=http://127.0.0.1:7420 -- /abs/erbrus wrap /data/runs/7/cmd.sh",
		"tmux set-option -t erbrus-webshop:1 remain-on-exit on",
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
	if !strings.Contains(rec.calls[1], "-t main:") {
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
	if rec.calls[0] != "tmux kill-window -t erbrus-webshop:3" {
		t.Errorf("calls = %v", rec.calls)
	}
}

func TestAlive(t *testing.T) {
	rec := &recorder{out: map[string]string{"tmux list-windows": "erbrus-webshop:1\nerbrus-webshop:3\n"}}
	tm := NewTmux(rec.run)
	if ok, _ := tm.Alive("erbrus-webshop:3"); !ok {
		t.Error("want alive")
	}
	if ok, _ := tm.Alive("erbrus-webshop:9"); ok {
		t.Error("want dead")
	}
	rec2 := &recorder{fail: map[string]error{"tmux list-windows": errors.New("no session")}}
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
		"tmux send-keys -t erbrus-x:3 -l fix the tests",
		"tmux send-keys -t erbrus-x:3 Enter",
	}
	if len(rec.calls) != 2 || rec.calls[0] != want[0] || rec.calls[1] != want[1] {
		t.Fatalf("calls = %q, want %q", rec.calls, want)
	}
}

func TestSendKeysError(t *testing.T) {
	rec := &recorder{fail: map[string]error{"tmux send-keys": errors.New("no window")}}
	tm := NewTmux(rec.run)
	if err := tm.Send(Handle("erbrus-x:3"), "hi"); err == nil {
		t.Fatal("want error when window is gone")
	}
}
