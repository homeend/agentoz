//go:build windows

package termview

// Term is a stub: erbrus.exe has no pty and no tmux; the server answers
// 501 and the page keeps the read-only screen.
type Term struct{}

func Start(argv []string, cols, rows int, env []string) (*Term, error) { return nil, ErrUnsupported }
func (t *Term) Read(p []byte) (int, error)                             { return 0, ErrUnsupported }
func (t *Term) Write(p []byte) (int, error)                            { return 0, ErrUnsupported }
func (t *Term) Resize(cols, rows int) error                            { return ErrUnsupported }
func (t *Term) Close() error                                           { return nil }
