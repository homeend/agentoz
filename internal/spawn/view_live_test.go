//go:build !windows

package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"erbrus/internal/termview"
)

// Runs only with ERBRUS_TMUX_TEST=1 against the local tmux server, in a
// throwaway session; it never touches other sessions.
func TestViewClientLive(t *testing.T) {
	if os.Getenv("ERBRUS_TMUX_TEST") != "1" {
		t.Skip("set ERBRUS_TMUX_TEST=1 to run against tmux")
	}
	sess := fmt.Sprintf("claudetest-term-%d", os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", sess, "-x", "120", "-y", "35", "cat").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", "="+sess).Run()
	out, err := exec.Command("tmux", "list-windows", "-t", sess, "-F", "#{session_name}:#{window_index}").Output()
	if err != nil {
		t.Fatal(err)
	}
	h := Handle(strings.TrimSpace(strings.Split(string(out), "\n")[0]))
	tm := NewTmux(ExecCmdRunner)

	cols, rows, err := tm.Size(h)
	if err != nil || cols != 120 || rows != 35 {
		t.Fatalf("size = %d x %d, %v", cols, rows, err)
	}
	view := ViewName(1)
	argv, _ := tm.ViewCommand(h, view)
	term, err := termview.Start(argv, 60, 15, termview.Env(os.Environ())) // deliberately smaller
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	time.Sleep(500 * time.Millisecond)
	if _, err := term.Write([]byte("typed-from-browser\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	pane, _ := exec.Command("tmux", "capture-pane", "-p", "-t", "="+string(h)).Output()
	if !strings.Contains(string(pane), "typed-from-browser") {
		t.Fatalf("input did not reach the window:\n%s", pane)
	}
	if c, r, _ := tm.Size(h); c != 120 || r != 35 {
		t.Fatalf("view client resized the window to %dx%d", c, r)
	}
	clients, _ := exec.Command("tmux", "list-clients", "-F", "#{client_session} #{client_flags}").Output()
	if !strings.Contains(string(clients), view+" ") || !strings.Contains(string(clients), "ignore-size") {
		t.Fatalf("view client not attached with ignore-size:\n%s", clients)
	}
	term.Close()
	time.Sleep(700 * time.Millisecond)
	if err := exec.Command("tmux", "has-session", "-t", "="+view).Run(); err == nil {
		t.Fatalf("view session %s survived the client", view)
	}

	// Guard: the viewed window dies while a second window exists in the
	// session. The view must die too, not switch to the other window.
	if out, err := exec.Command("tmux", "new-window", "-d", "-t", sess, "cat").CombinedOutput(); err != nil {
		t.Fatalf("new-window: %v %s", err, out)
	}
	view2 := ViewName(2)
	argv2, _ := tm.ViewCommand(h, view2)
	term2, err := termview.Start(argv2, 60, 15, termview.Env(os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	defer term2.Close()
	time.Sleep(500 * time.Millisecond)
	if err := tm.GuardView(view2); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "kill-window", "-t", "="+string(h)).Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if err := exec.Command("tmux", "has-session", "-t", "="+view2).Run(); err == nil {
		t.Fatalf("view %s survived its window and now shows another one", view2)
	}
	if _, _, err := tm.Size(h); err == nil {
		t.Fatal("Size must fail for the killed window")
	}
}
