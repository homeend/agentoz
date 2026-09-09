package spawn

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestViewCommand(t *testing.T) {
	rec := &recorder{}
	argv, err := NewTmux(rec.run).ViewCommand("erbrus-web:7", "erbrus-view-12-a1b2c3d4")
	if err != nil {
		t.Fatal(err)
	}
	want := "tmux new-session -s erbrus-view-12-a1b2c3d4 -n erbrus-placeholder -f ignore-size sleep 2147483647" +
		" ; set -t erbrus-view-12-a1b2c3d4 status off" +
		" ; set -t erbrus-view-12-a1b2c3d4 destroy-unattached on" +
		" ; set -t erbrus-view-12-a1b2c3d4 prefix None" +
		" ; set -t erbrus-view-12-a1b2c3d4 prefix2 None" +
		" ; link-window -s =erbrus-web:7 -t =erbrus-view-12-a1b2c3d4:" +
		" ; kill-window -t =erbrus-view-12-a1b2c3d4:erbrus-placeholder"
	if got := strings.Join(argv, " "); got != want {
		t.Errorf("argv =\n%s\nwant\n%s", got, want)
	}
	if len(rec.calls) != 0 {
		t.Errorf("ViewCommand must not run tmux, ran %v", rec.calls)
	}
	if _, err := NewTmux(rec.run).ViewCommand("nocolon", "v"); err == nil {
		t.Error("malformed handle accepted")
	}
}

func TestSize(t *testing.T) {
	// Window 7 among others: only its own line counts.
	rec := &recorder{out: map[string]string{"tmux list-windows": "1 134 36\n7 120 35\n9 80 24\n"}}
	cols, rows, err := NewTmux(rec.run).Size("erbrus-web:7")
	if err != nil || cols != 120 || rows != 35 {
		t.Fatalf("size = %d x %d, %v", cols, rows, err)
	}
	if rec.calls[0] != "tmux list-windows -t =erbrus-web -F #{window_index} #{window_width} #{window_height}" {
		t.Errorf("call = %q", rec.calls[0])
	}
	// Window gone while the session lives: an error, never another
	// window's size (tmux display would silently answer for the current
	// window; that is how a browser terminal ended up on the user's shell).
	rec = &recorder{out: map[string]string{"tmux list-windows": "1 134 36\n"}}
	if _, _, err := NewTmux(rec.run).Size("erbrus-web:7"); err == nil || !strings.Contains(err.Error(), "window gone") {
		t.Errorf("missing window: %v", err)
	}
	rec = &recorder{fail: map[string]error{"tmux list-windows": errors.New("no such session")}}
	if _, _, err := NewTmux(rec.run).Size("erbrus-web:7"); err == nil {
		t.Error("missing session must error")
	}
	rec = &recorder{out: map[string]string{"tmux list-windows": "7 x y\n"}}
	if _, _, err := NewTmux(rec.run).Size("erbrus-web:7"); err == nil {
		t.Error("garbage must error")
	}
	if _, _, err := NewTmux(rec.run).Size("nocolon"); err == nil {
		t.Error("malformed handle accepted")
	}
}

func TestViewName(t *testing.T) {
	a, b := ViewName(12), ViewName(12)
	if !regexp.MustCompile(`^erbrus-view-12-[0-9a-f]{8}$`).MatchString(a) {
		t.Errorf("name = %q", a)
	}
	if a == b {
		t.Errorf("two names for one run must differ: %q", a)
	}
}

func TestSweepViewsKillsOnlyUnattachedViews(t *testing.T) {
	rec := &recorder{out: map[string]string{
		"tmux list-sessions": "erbrus-web 1\nerbrus-view-3-aaaaaaaa 0\nerbrus-view-4-bbbbbbbb 1\nother-view 0\n",
	}}
	killed := NewTmux(rec.run).SweepViews()
	if strings.Join(killed, ",") != "erbrus-view-3-aaaaaaaa" {
		t.Errorf("killed = %v", killed)
	}
	if len(rec.calls) != 2 || rec.calls[1] != "tmux kill-session -t =erbrus-view-3-aaaaaaaa" {
		t.Errorf("calls = %v", rec.calls)
	}
	// No tmux server: nothing to do, no error.
	rec = &recorder{fail: map[string]error{"tmux list-sessions": errors.New("no server")}}
	if killed := NewTmux(rec.run).SweepViews(); len(killed) != 0 {
		t.Errorf("killed without server: %v", killed)
	}
}
