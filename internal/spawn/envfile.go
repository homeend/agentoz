package spawn

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The env file carries a run's ERBRUS_* variables to `erbrus wrap` on
// every platform: WezTerm's spawn has no env flag, tmux's -e stays as a
// courtesy to humans in shell windows.

func WriteEnvFile(runDir string, env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + env[k] + "\n")
	}
	return os.WriteFile(filepath.Join(runDir, "env"), []byte(b.String()), 0o600)
}

// ReadEnvFile returns KEY=VALUE lines from <dir of cmdFile>/env; nil when
// there is no such file.
func ReadEnvFile(cmdFile string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(cmdFile), "env"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimRight(line, "\r"); strings.Contains(line, "=") {
			out = append(out, line)
		}
	}
	return out, nil
}
