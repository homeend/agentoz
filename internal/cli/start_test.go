package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// startTestRepo creates a real git repo and points ERBRUS_* env at a live
// serve instance (pattern from TestInitAutoConfigures).
func startTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "startrepo")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	t.Setenv("ERBRUS_HOME", tmp)
	cfgPath := filepath.Join(tmp, "config.yaml")
	// The "sh" provider makes --fg runs real but instant.
	os.WriteFile(cfgPath, []byte(`port: 0
data_dir: `+tmp+`
providers:
  sh:
    command: 'true {args} "{prompt}"'
presets:
  quick:
    provider: sh
`), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfgPath)

	addrCh := make(chan string, 1)
	serveAddrHook = func(addr string) { addrCh <- addr }
	t.Cleanup(func() { serveAddrHook = nil })
	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"serve"}, &out, &errOut)
	}()
	t.Setenv("ERBRUS_URL", "http://"+<-addrCh)

	oldWd, _ := os.Getwd()
	os.Chdir(repo)
	t.Cleanup(func() { os.Chdir(oldWd) })
	return repo
}

func TestStartFgRunsAndReports(t *testing.T) {
	startTestRepo(t)
	var out, errOut bytes.Buffer
	code := Run([]string{"start", "quick", "--fg", "--prompt", "do nothing"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "created project startrepo") {
		t.Errorf("auto-configure not announced: %q", out.String())
	}
	if !strings.Contains(out.String(), "exit 0") {
		t.Errorf("fg completion not announced: %q", out.String())
	}
}

func TestStartUnknownPreset(t *testing.T) {
	startTestRepo(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"start", "nope", "--fg"}, &out, &errOut); code == 0 {
		t.Fatal("want non-zero exit")
	}
	if !strings.Contains(errOut.String(), "nope") {
		t.Errorf("stderr should name the preset: %q", errOut.String())
	}
}

func TestStartNoCreateFailsOnUnknownRepo(t *testing.T) {
	startTestRepo(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"start", "quick", "--fg", "--no-create"}, &out, &errOut); code == 0 {
		t.Fatal("want failure: project not registered and creation disabled")
	}
	if !strings.Contains(errOut.String(), "no-create") && !strings.Contains(errOut.String(), "not registered") {
		t.Errorf("stderr: %q", errOut.String())
	}
}

// TestStartOutsideRepo runs start from a plain temp dir with no server
// reachable and no git repo present. GitRoot fails first, so the CLI must
// surface a "not inside a git repository" error before ever attempting the
// HTTP call.
func TestStartOutsideRepo(t *testing.T) {
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:1")
	oldWd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() { os.Chdir(oldWd) })

	var out, errOut bytes.Buffer
	if code := Run([]string{"start", "quick"}, &out, &errOut); code == 0 {
		t.Fatal("want failure without a git repo")
	}
	if !strings.Contains(errOut.String(), "git") {
		t.Errorf("stderr should mention git: %q", errOut.String())
	}
}
