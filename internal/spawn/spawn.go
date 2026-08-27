// Package spawn launches agent runs into terminal multiplexers. TmuxSpawner
// is the v1 implementation; zellij/headless are future Spawners. erbrus
// only ever kills windows it created.
package spawn

import "os/exec"

type CmdRunner func(name string, args ...string) ([]byte, error)

func ExecCmdRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type RunSpec struct {
	Session       string
	AttachSession string
	WindowName    string
	Workdir       string
	Env           map[string]string
	Command       []string
}

type Handle string

type Spawner interface {
	Spawn(spec RunSpec) (Handle, error)
	Stop(h Handle) error
	Alive(h Handle) (bool, error)
}
