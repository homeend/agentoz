//go:build !windows

package termview

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Term is one command running in a pty. Read/Write move terminal bytes;
// Close ends the command and releases the pty.
type Term struct {
	f    *os.File
	cmd  *exec.Cmd
	once sync.Once
	werr error
}

// Start runs argv in a new pty of the given size. env nil means the
// parent environment (callers should pass Env(os.Environ())).
func Start(argv []string, cols, rows int, env []string) (*Term, error) {
	if len(argv) == 0 {
		return nil, errors.New("termview: empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &Term{f: f, cmd: cmd}, nil
}

func (t *Term) Read(p []byte) (int, error)  { return t.f.Read(p) }
func (t *Term) Write(p []byte) (int, error) { return t.f.Write(p) }

func (t *Term) Resize(cols, rows int) error {
	return pty.Setsize(t.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close kills the command, closes the pty and reaps the process. Safe to
// call more than once.
func (t *Term) Close() error {
	t.once.Do(func() {
		if t.cmd.Process != nil {
			t.cmd.Process.Kill()
		}
		t.werr = t.f.Close()
		t.cmd.Wait()
	})
	return t.werr
}
