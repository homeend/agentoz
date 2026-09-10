package spawn

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// wrec records stdin-runner calls as "stdin|name args…" and replays
// scripted stdout by command prefix (on "name args…").
type wrec struct {
	calls []string
	out   map[string]string
	fail  map[string]error
	// seq lets one prefix answer differently per call (Send retries).
	seq map[string][]string
}

func (r *wrec) run(stdin, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, stdin+"|"+call)
	for p, err := range r.fail {
		if strings.HasPrefix(call, p) {
			return nil, err
		}
	}
	for p, outs := range r.seq {
		if strings.HasPrefix(call, p) && len(outs) > 0 {
			o := outs[0]
			r.seq[p] = outs[1:]
			return []byte(o), nil
		}
	}
	for p, o := range r.out {
		if strings.HasPrefix(call, p) {
			return []byte(o), nil
		}
	}
	return nil, nil
}

const listJSON = `[
 {"window_id":0,"tab_id":0,"pane_id":0,"workspace":"default","size":{"rows":24,"cols":80},"title":"cmd.exe","cwd":"file:///C:/Users/homee/","tab_title":""},
 {"window_id":1,"tab_id":1,"pane_id":7,"workspace":"erbrus-webshop","size":{"rows":40,"cols":120},"title":"claude","cwd":"file:///T:/code/webshop/","tab_title":"webshop/claude"}
]`

func wspec() RunSpec {
	return RunSpec{Session: "erbrus-webshop", WindowName: "webshop/claude", Workdir: `T:\code\webshop`,
		Env:     map[string]string{"ERBRUS_URL": "http://127.0.0.1:7421"},
		Command: []string{`T:\erbrus\bin\erbrus.exe`, "wrap", `T:\data\runs\7\cmd.json`}}
}

func TestWeztermSpawnNewWindowThenTitle(t *testing.T) {
	r := &wrec{out: map[string]string{"wezterm cli spawn": "7\n"}}
	w := NewWezterm(r.run, "wezterm")
	h, err := w.Spawn(wspec())
	if err != nil || h != "7@webshop/claude" {
		t.Fatalf("h = %q, %v", h, err)
	}
	want := []string{
		`|wezterm cli spawn --new-window --workspace erbrus-webshop --cwd T:\code\webshop -- T:\erbrus\bin\erbrus.exe wrap T:\data\runs\7\cmd.json`,
		`|wezterm cli set-tab-title --pane-id 7 webshop/claude`,
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestWeztermSpawnUsesAttachSessionAsWorkspaceAndReportsStderr(t *testing.T) {
	r := &wrec{out: map[string]string{`C:\Users\homee\bin\WezTerm\wezterm.exe cli spawn`: "3\n"}}
	w := NewWezterm(r.run, `C:\Users\homee\bin\WezTerm\wezterm.exe`)
	sp := wspec()
	sp.AttachSession = "shared"
	if _, err := w.Spawn(sp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.calls[0], `C:\Users\homee\bin\WezTerm\wezterm.exe cli spawn --new-window --workspace shared `) {
		t.Errorf("call = %q", r.calls[0])
	}
	r2 := &wrec{fail: map[string]error{"wezterm cli spawn": errors.New("exit status 1")}}
	if _, err := NewWezterm(r2.run, "wezterm").Spawn(wspec()); err == nil || !strings.Contains(err.Error(), "spawn") {
		t.Errorf("err = %v", err)
	}
}

func TestWeztermAliveMatchesIdAndTitle(t *testing.T) {
	r := &wrec{out: map[string]string{"wezterm cli list": listJSON}}
	w := NewWezterm(r.run, "wezterm")
	for h, want := range map[Handle]bool{"7@webshop/claude": true, "7@other": false, "9@webshop/claude": false} {
		if ok, err := w.Alive(h); err != nil || ok != want {
			t.Errorf("Alive(%s) = %v, %v; want %v", h, ok, err, want)
		}
	}
	r2 := &wrec{fail: map[string]error{"wezterm cli list": errors.New("boom")}}
	if _, err := NewWezterm(r2.run, "wezterm").Alive("7@webshop/claude"); err == nil {
		t.Error("a failed list is an error, not 'dead'")
	}
}

func TestWeztermCaptureScreenAndSize(t *testing.T) {
	r := &wrec{out: map[string]string{
		"wezterm cli list":     listJSON,
		"wezterm cli get-text": "T:\\code\\webshop>echo hello\nhello\n",
	}}
	w := NewWezterm(r.run, "wezterm")
	sc, err := w.Capture("7@webshop/claude")
	if err != nil || sc.Cols != 120 || sc.Rows != 40 || sc.Dead || !sc.Activity.IsZero() {
		t.Fatalf("sc = %+v, %v", sc, err)
	}
	if !strings.Contains(sc.Raw, "echo hello") {
		t.Errorf("raw = %q", sc.Raw)
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "|wezterm cli get-text --escapes --pane-id 7") {
		t.Errorf("calls = %q", r.calls)
	}
	// Gone pane: not in the list → error, no get-text.
	if _, err := w.Capture("9@x"); err == nil {
		t.Error("capture of a gone pane must fail")
	}
}

func TestWeztermSendPastesOnStdinThenEnter(t *testing.T) {
	sendSettle, sendVerifyDelay = 0, 0
	r := &wrec{out: map[string]string{"wezterm cli get-text": "❯ \n"}}
	w := NewWezterm(r.run, "wezterm")
	if err := w.Send("7@webshop/claude", "hello\nworld"); err != nil {
		t.Fatal(err)
	}
	if r.calls[0] != "hello\nworld|wezterm cli send-text --pane-id 7" {
		t.Errorf("paste = %q", r.calls[0])
	}
	if r.calls[1] != "\r|wezterm cli send-text --no-paste --pane-id 7" {
		t.Errorf("enter = %q", r.calls[1])
	}
}

func TestWeztermSendRetriesWhileTextSitsInInputBox(t *testing.T) {
	sendSettle, sendVerifyDelay = 0, 0
	pending := "──────────\n❯ hello\n"
	r := &wrec{seq: map[string][]string{"wezterm cli get-text": {pending, pending, "❯ \n"}}}
	w := NewWezterm(r.run, "wezterm")
	if err := w.Send("7@x", "hello"); err != nil {
		t.Fatal(err)
	}
	enters := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, "\r|") {
			enters++
		}
	}
	if enters != 3 {
		t.Errorf("enters = %d, want 3", enters)
	}
}

func TestWeztermSendKeys(t *testing.T) {
	r := &wrec{}
	w := NewWezterm(r.run, "wezterm")
	for key, want := range map[string]string{"Enter": "\r", "Escape": "\x1b", "Up": "\x1b[A", "Down": "\x1b[B", "Tab": "\t", "3": "3"} {
		r.calls = nil
		if err := w.SendKeys("7@x", key); err != nil {
			t.Fatal(err)
		}
		if r.calls[0] != want+"|wezterm cli send-text --no-paste --pane-id 7" {
			t.Errorf("%s → %q", key, r.calls[0])
		}
	}
	if err := w.SendKeys("7@x", "C-c"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %v", err)
	}
}

func TestWeztermStopAndOpenTerminal(t *testing.T) {
	r := &wrec{}
	w := NewWezterm(r.run, "wezterm")
	if err := w.Stop("7@x"); err != nil || r.calls[0] != "|wezterm cli kill-pane --pane-id 7" {
		t.Fatalf("stop: %v %q", err, r.calls)
	}
	launch, ok := w.OpenTerminal(`T:\code\webshop`, []string{"powershell"})
	if !ok || !reflect.DeepEqual(launch, []string{"wezterm", "start", "--cwd", `T:\code\webshop`, "--", "powershell"}) {
		t.Errorf("open = %q %v", launch, ok)
	}
}

func TestNewDriverPicksWeztermOnWindows(t *testing.T) {
	d, err := NewDriver("", "wezterm", "windows", nil, (&wrec{}).run)
	if err != nil || d.Name() != "wezterm" {
		t.Fatalf("driver = %v, %v", d, err)
	}
	if _, ok := d.Viewer(); ok {
		t.Error("wezterm has no browser terminal")
	}
	if !d.PromptByPaste() {
		t.Error("windows host pastes")
	}
	if launch, ok := d.OpenTerminal("/x", []string{"gg"}); !ok || launch[0] != "wezterm" {
		t.Errorf("open = %q %v", launch, ok)
	}
	// Explicit wezterm on linux is allowed too (posix host).
	d, err = NewDriver("wezterm", "/opt/wezterm", "linux", nil, (&wrec{}).run)
	if err != nil || d.Name() != "wezterm" || d.PromptByPaste() {
		t.Errorf("linux+wezterm = %v %v", d, err)
	}
}
