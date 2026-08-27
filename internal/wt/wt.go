// Package wt detects git worktrees, preferring the user's `wt` tool
// (`wt list --json -r <root>`) and falling back to
// `git worktree list --porcelain`.
package wt

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	IsMain bool   `json:"is_main"`
}

type Runner func(dir, name string, args ...string) ([]byte, error)

func ExecRunner(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

func List(run Runner, wtBin, repoRoot string) ([]Worktree, error) {
	if out, err := run(repoRoot, wtBin, "list", "--json", "-r", repoRoot); err == nil {
		var wts []Worktree
		if jerr := json.Unmarshal(out, &wts); jerr == nil && len(wts) > 0 {
			return wts, nil
		}
	}
	out, err := run(repoRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("worktree detection failed (wt and git): %w", err)
	}
	return ParsePorcelain(out), nil
}

// ParsePorcelain parses `git worktree list --porcelain`. The first entry is
// the main worktree.
func ParsePorcelain(out []byte) []Worktree {
	var wts []Worktree
	var cur *Worktree
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if cur != nil {
				wts = append(wts, *cur)
			}
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			if cur != nil {
				cur.Head = strings.TrimPrefix(line, "HEAD ")
			}
		case strings.HasPrefix(line, "branch "):
			if cur != nil {
				cur.Branch = strings.TrimPrefix(line, "branch ")
			}
		}
	}
	if cur != nil {
		wts = append(wts, *cur)
	}
	if len(wts) > 0 {
		wts[0].IsMain = true
	}
	return wts
}

func GitRoot(run Runner, dir string) (string, error) {
	out, err := run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// MainRoot finds the main worktree's path for the repo containing dir.
func MainRoot(run Runner, dir string) (string, error) {
	out, err := run(dir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	wts := ParsePorcelain(out)
	if len(wts) == 0 {
		return "", fmt.Errorf("no worktrees found from %s", dir)
	}
	return wts[0].Path, nil
}

func ShortBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}
