package wt

import (
	"errors"
	"fmt"
	"testing"
)

// fake returns a Runner serving canned outputs keyed by "name args...".
func fake(outputs map[string]string) Runner {
	return func(dir, name string, args ...string) ([]byte, error) {
		key := name
		for _, a := range args {
			key += " " + a
		}
		if out, ok := outputs[key]; ok {
			return []byte(out), nil
		}
		return nil, fmt.Errorf("no fake for %q", key)
	}
}

const wtJSON = `[
  {"path": "/repo", "branch": "refs/heads/main", "head": "abc", "is_main": true},
  {"path": "/repo-wt/feat", "branch": "refs/heads/wt/feat", "head": "def", "is_main": false}
]`

func TestListViaWt(t *testing.T) {
	run := fake(map[string]string{"wt list --json -r /repo": wtJSON})
	wts, err := List(run, "wt", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("len = %d, want 2", len(wts))
	}
	if !wts[0].IsMain || wts[0].Path != "/repo" {
		t.Errorf("main worktree wrong: %+v", wts[0])
	}
	if wts[1].Branch != "refs/heads/wt/feat" {
		t.Errorf("branch = %q", wts[1].Branch)
	}
}

const porcelain = `worktree /repo
HEAD abcabcabc
branch refs/heads/main

worktree /repo-wt/feat
HEAD defdefdef
branch refs/heads/wt/feat

`

func TestListFallsBackToGit(t *testing.T) {
	run := func(dir, name string, args ...string) ([]byte, error) {
		if name == "wt" {
			return nil, errors.New("wt: not found")
		}
		return []byte(porcelain), nil
	}
	wts, err := List(run, "wt", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("len = %d, want 2", len(wts))
	}
	if !wts[0].IsMain {
		t.Error("first porcelain entry is the main worktree")
	}
	if wts[1].IsMain {
		t.Error("second entry must not be main")
	}
}

func TestListBothFail(t *testing.T) {
	run := func(dir, name string, args ...string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	if _, err := List(run, "wt", "/repo"); err == nil {
		t.Fatal("want error when wt and git both fail")
	}
}

func TestParsePorcelainDetachedHead(t *testing.T) {
	out := []byte("worktree /repo\nHEAD abc\ndetached\n\n")
	wts := ParsePorcelain(out)
	if len(wts) != 1 || wts[0].Branch != "" {
		t.Errorf("detached entry parsed wrong: %+v", wts)
	}
}

func TestGitRoot(t *testing.T) {
	run := fake(map[string]string{"git rev-parse --show-toplevel": "/repo-wt/feat\n"})
	root, err := GitRoot(run, "/repo-wt/feat/sub")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/repo-wt/feat" {
		t.Errorf("root = %q", root)
	}
}

func TestMainRoot(t *testing.T) {
	run := fake(map[string]string{"git worktree list --porcelain": porcelain})
	root, err := MainRoot(run, "/repo-wt/feat")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/repo" {
		t.Errorf("main root = %q", root)
	}
}

func TestShortBranch(t *testing.T) {
	if got := ShortBranch("refs/heads/wt/feat"); got != "wt/feat" {
		t.Errorf("ShortBranch = %q", got)
	}
	if got := ShortBranch("main"); got != "main" {
		t.Errorf("ShortBranch = %q", got)
	}
}
