package gg

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Recorded from gg 2026-09-10 in a scratch repo (spec table).
const branchLs = "  feat/one\n  feat/one-2026-09-10_02-40\n* main ↑1 ↓2\n  feat/two\n"
const wtList = "main\t/r/ggrepo\nfeat/three\t/r/ggrepo.worktrees/feat-three\n"
const addOK = "→ creating worktree: feat/three → /r/ggrepo.worktrees/feat-three\n✓ created worktree feat/three at /r/ggrepo.worktrees/feat-three\n"
const addTaken = "error: create worktree: branch feat/three is already checked out in worktree /r/ggrepo.worktrees/feat-three\n"

type call struct {
	dir  string
	args []string
}

func script(t *testing.T, out string, err error) (Runner, *[]call) {
	t.Helper()
	var calls []call
	return func(dir, name string, args ...string) ([]byte, error) {
		if name != "gg" {
			t.Fatalf("ran %s, want gg", name)
		}
		calls = append(calls, call{dir, args})
		return []byte(out), err
	}, &calls
}

func TestBranches(t *testing.T) {
	run, calls := script(t, branchLs, nil)
	got, err := Branches(run, "gg", "/r/ggrepo")
	if err != nil {
		t.Fatal(err)
	}
	want := []Branch{{"feat/one", false}, {"feat/one-2026-09-10_02-40", false}, {"main", true}, {"feat/two", false}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("branch %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if c := (*calls)[0]; c.dir != "/r/ggrepo" || strings.Join(c.args, " ") != "branch ls" {
		t.Fatalf("call = %+v", c)
	}
}

func TestWorktrees(t *testing.T) {
	run, _ := script(t, wtList, nil)
	got, err := Worktrees(run, "gg", "/r/ggrepo")
	if err != nil || len(got) != 2 || got[1] != (Worktree{"feat/three", "/r/ggrepo.worktrees/feat-three"}) {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestAddFromParsesCreatedLine(t *testing.T) {
	run, calls := script(t, addOK, nil)
	c, err := AddFrom(run, "gg", "/r/ggrepo", "feat/two", "feat/three")
	if err != nil || c != (Created{"feat/three", "/r/ggrepo.worktrees/feat-three"}) {
		t.Fatalf("got %+v err %v", c, err)
	}
	if strings.Join((*calls)[0].args, " ") != "worktree add --from feat/two feat/three" {
		t.Fatalf("args = %v", (*calls)[0].args)
	}
}

func TestAddForBranchArgsAndError(t *testing.T) {
	run, calls := script(t, addTaken, errors.New("exit status 1"))
	_, err := AddForBranch(run, "gg", "/r/ggrepo", "feat/three")
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatalf("err = %T %v", err, err)
	}
	if ge.Message != "create worktree: branch feat/three is already checked out in worktree /r/ggrepo.worktrees/feat-three" {
		t.Fatalf("message = %q", ge.Message)
	}
	if strings.Join((*calls)[0].args, " ") != "worktree add --branch feat/three" {
		t.Fatalf("args = %v", (*calls)[0].args)
	}
}

func TestAddSuccessWithoutCreatedLine(t *testing.T) {
	run, _ := script(t, "something else\n", nil)
	_, err := AddForBranch(run, "gg", "/r", "b")
	if err == nil || !strings.Contains(err.Error(), "did not report the new worktree") {
		t.Fatalf("err = %v", err)
	}
}

func TestMessage(t *testing.T) {
	if m := Message("gg", nil, exec.ErrNotFound); m != `gg not found (gg_bin = "gg"): install gg or set gg_bin in config.yaml` {
		t.Fatal(m)
	}
	// exec wraps the sentinel in *exec.Error; errors.Is must still see it.
	if m := Message("gg", nil, &exec.Error{Name: "gg", Err: exec.ErrNotFound}); !strings.HasPrefix(m, "gg not found") {
		t.Fatal(m)
	}
	if m := Message("gg", []byte("noise\nerror: create worktree: no local branch \"x\"\n"), errors.New("exit status 1")); m != `create worktree: no local branch "x"` {
		t.Fatal(m)
	}
	if m := Message("gg", []byte("  plain failure  \n"), errors.New("exit status 1")); m != "plain failure" {
		t.Fatal(m)
	}
	if m := Message("gg", nil, errors.New("signal: killed")); m != "signal: killed" {
		t.Fatal(m)
	}
}
