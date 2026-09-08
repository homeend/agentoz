//go:build !windows

package termview

import (
	"os"
	"strings"
	"testing"
	"time"
)

// readUntil collects output until want appears or the deadline passes.
func readUntil(t *testing.T, term *Term, want string, d time.Duration) string {
	t.Helper()
	var sb strings.Builder
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				sb.WriteString(string(buf[:n]))
				if strings.Contains(sb.String(), want) {
					close(done)
					return
				}
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
	return sb.String()
}

func TestStartSizeEchoResizeClose(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	term, err := Start([]string{"sh", "-c", "stty size; cat"}, 80, 24, Env(os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, term, "24 80", 3*time.Second); !strings.Contains(got, "24 80") {
		t.Fatalf("initial size not applied, output %q", got)
	}
	if _, err := term.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, term, "hello", 3*time.Second); !strings.Contains(got, "hello") {
		t.Fatalf("no echo, output %q", got)
	}
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := term.Write([]byte("x")); err == nil {
		t.Error("write after close must fail")
	}
}

func TestResizeIsSeenByChild(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	// The child prints its size once it reads a line, so the resize lands first.
	term, err := Start([]string{"sh", "-c", "read x; stty size"}, 80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	term.Write([]byte("go\n"))
	if got := readUntil(t, term, "30 100", 3*time.Second); !strings.Contains(got, "30 100") {
		t.Fatalf("resize not applied, output %q", got)
	}
}

func TestEnvAddsTerminalVars(t *testing.T) {
	env := Env([]string{"HOME=/h", "TERM=dumb"})
	j := strings.Join(env, "\n")
	if !strings.Contains(j, "TERM=xterm-256color") || !strings.Contains(j, "COLORTERM=truecolor") || !strings.Contains(j, "HOME=/h") || strings.Contains(j, "TERM=dumb") {
		t.Errorf("env = %v", env)
	}
}

func TestStartRejectsEmpty(t *testing.T) {
	if _, err := Start(nil, 80, 24, nil); err == nil {
		t.Error("empty argv accepted")
	}
}
