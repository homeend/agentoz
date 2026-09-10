// Package gg runs gigagit (gg) for worktree creation on the happy path.
// Verified behaviour (2026-09-10, gg 26.x, no TTY, stdin closed) is in
// docs/superpowers/specs/2026-09-10-worktree-create-design.md:
//
//	gg branch ls                      "* main ↑1 ↓2" / "  feat/x" per line
//	gg worktree list                  "<branch>\t<path>" per line
//	gg worktree add --branch <b>      existing local branch → new worktree
//	gg worktree add --from <base> <n> new branch at base + worktree
//
// Success prints "✓ created worktree <branch> at <path>"; failures print
// "error: …" and exit 1. The bare "gg worktree add <x>" creates a dated
// NEW branch and is never used here.
package gg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Runner executes name with args in dir and returns the combined output.
// Same shape as wt.Runner so the server's runner serves both packages.
type Runner func(dir, name string, args ...string) ([]byte, error)

// Timeout bounds one gg invocation; gg never prompts without a TTY, so a
// long run means git itself is stuck.
const Timeout = 60 * time.Second

// ExecRunner runs the command with stdin closed and stderr merged.
func ExecRunner(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("gg timed out after %s", Timeout)
	}
	return out, err
}

type Branch struct {
	Name    string
	Current bool
}

type Worktree struct {
	Branch, Path string
}

type Created struct {
	Branch, Path string
}

// Error is a failed gg invocation: Message is the line for humans.
type Error struct {
	Message string
	Output  string
	Err     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// Message turns gg's output and exit error into one human line.
func Message(bin string, out []byte, err error) string {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Sprintf("gg not found (gg_bin = %q): install gg or set gg_bin in config.yaml", bin)
	}
	// A runner that captures stdout only (wt.ExecRunner uses cmd.Output)
	// leaves gg's "error: …" line in the exit error's Stderr.
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		out = append(append([]byte{}, out...), ee.Stderr...)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "error: ") {
			return strings.TrimPrefix(l, "error: ")
		}
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	if err != nil {
		return err.Error()
	}
	return "gg failed without output"
}

func exec1(run Runner, bin, root string, args ...string) ([]byte, error) {
	out, err := run(root, bin, args...)
	if err != nil {
		return out, &Error{Message: Message(bin, out, err), Output: string(out), Err: err}
	}
	return out, nil
}

// Branches lists local branches; Current marks HEAD's branch.
func Branches(run Runner, bin, root string) ([]Branch, error) {
	out, err := exec1(run, bin, root, "branch", "ls")
	if err != nil {
		return nil, err
	}
	var bs []Branch
	for _, l := range strings.Split(string(out), "\n") {
		cur := strings.HasPrefix(l, "* ")
		l = strings.TrimSpace(strings.TrimPrefix(l, "* "))
		if l == "" {
			continue
		}
		// "main ↑1 ↓2": the name is the first field.
		bs = append(bs, Branch{Name: strings.Fields(l)[0], Current: cur})
	}
	return bs, nil
}

// Worktrees lists checked-out worktrees (main first).
func Worktrees(run Runner, bin, root string) ([]Worktree, error) {
	out, err := exec1(run, bin, root, "worktree", "list")
	if err != nil {
		return nil, err
	}
	var ws []Worktree
	for _, l := range strings.Split(string(out), "\n") {
		br, path, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		ws = append(ws, Worktree{Branch: strings.TrimSpace(br), Path: strings.TrimSpace(path)})
	}
	return ws, nil
}

var createdRe = regexp.MustCompile(`created worktree (.+?) at (.+)$`)

func add(run Runner, bin, root string, args ...string) (Created, error) {
	out, err := exec1(run, bin, root, append([]string{"worktree", "add"}, args...)...)
	if err != nil {
		return Created{}, err
	}
	for _, l := range strings.Split(string(out), "\n") {
		if m := createdRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			return Created{Branch: m[1], Path: strings.TrimSpace(m[2])}, nil
		}
	}
	return Created{}, &Error{Message: "gg did not report the new worktree: " + strings.TrimSpace(string(out)), Output: string(out)}
}

// AddForBranch checks an existing local branch out into a new worktree.
func AddForBranch(run Runner, bin, root, branch string) (Created, error) {
	return add(run, bin, root, "--branch", branch)
}

// AddFrom creates branch name at base and a worktree on it, in one step.
func AddFrom(run Runner, bin, root, base, name string) (Created, error) {
	return add(run, bin, root, "--from", base, name)
}
