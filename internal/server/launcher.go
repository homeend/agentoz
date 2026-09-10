package server

import "os/exec"

// Launcher starts a GUI tool detached in dir; only Start's error is ever
// reported (spec: editors outlive erbrus, nothing is tracked afterwards).
// argv is executed directly, without a shell, so a missing binary is a
// Start error rather than a silent failure inside `sh -c`.
type Launcher interface {
	Start(dir string, argv []string) error
}

// SetLauncher wires the GUI-tool launcher (serve: ExecLauncher()).
func (s *Server) SetLauncher(l Launcher) { s.launcher = l }

// ExecLauncher is the real launcher.
func ExecLauncher() Launcher { return execLauncher{} }

type execLauncher struct{}

func (execLauncher) Start(dir string, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
