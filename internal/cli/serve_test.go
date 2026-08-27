package cli

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/config"
)

// TestServeStartsAndServes boots serve on a random port in a goroutine and
// hits /api/projects.
func TestServeStartsAndServes(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 0\ndata_dir: "+tmp+"\n"), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfgPath)

	addrCh := make(chan string, 1)
	serveAddrHook = func(addr string) { addrCh <- addr }
	defer func() { serveAddrHook = nil }()

	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"serve"}, &out, &errOut)
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not report an address")
	}
	resp, err := http.Get("http://" + addr + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestInitAutoConfigures runs init inside a real temp git repo against a
// live serve instance.
func TestInitAutoConfigures(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "myrepo")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"commit", "--allow-empty", "-m", "x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}

	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 0\ndata_dir: "+tmp+"\n"), 0o644)
	t.Setenv("ERBRUS_CONFIG", cfgPath)
	t.Setenv("ERBRUS_HOME", tmp)

	addrCh := make(chan string, 1)
	serveAddrHook = func(addr string) { addrCh <- addr }
	defer func() { serveAddrHook = nil }()
	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"serve"}, &out, &errOut)
	}()
	addr := <-addrCh
	t.Setenv("ERBRUS_URL", "http://"+addr)

	oldWd, _ := os.Getwd()
	os.Chdir(repo)
	defer os.Chdir(oldWd)

	var out, errOut bytes.Buffer
	code := Run([]string{"init"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("init exit = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "myrepo") {
		t.Errorf("output %q should announce project name", out.String())
	}
	if !strings.Contains(out.String(), "general") {
		t.Errorf("output %q should announce general channel", out.String())
	}
	if _, err := os.Stat(config.RepoConfigPath(repo)); err != nil {
		t.Error(".erbrus.yaml not scaffolded at new location")
	}

	// Second init: idempotent, no scaffold overwrite.
	os.WriteFile(config.RepoConfigPath(repo), []byte("session: custom\n"), 0o644)
	out.Reset()
	if code := Run([]string{"init"}, &out, &errOut); code != 0 {
		t.Fatalf("second init failed: %s", errOut.String())
	}
	data, _ := os.ReadFile(config.RepoConfigPath(repo))
	if string(data) != "session: custom\n" {
		t.Error("init must not overwrite an existing .erbrus.yaml")
	}
}
