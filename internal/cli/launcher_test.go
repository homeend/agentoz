package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func launcherTestEnv(t *testing.T, pathDirs ...string) (home, bin string) {
	t.Helper()
	home = t.TempDir()
	bin = filepath.Join(t.TempDir(), "erbrus")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	oldExe, oldHome, oldEnv := launcherExecutable, launcherHomeDir, launcherGetenv
	launcherExecutable = func() (string, error) { return bin, nil }
	launcherHomeDir = func() (string, error) { return home, nil }
	pathEnv := strings.Join(pathDirs, string(os.PathListSeparator))
	launcherGetenv = func(k string) string {
		if k == "PATH" {
			return pathEnv
		}
		return ""
	}
	t.Cleanup(func() { launcherExecutable, launcherHomeDir, launcherGetenv = oldExe, oldHome, oldEnv })
	return home, bin
}

func launcherName() string {
	if runtime.GOOS == "windows" {
		return "erbrus.cmd"
	}
	return "erbrus"
}

func TestLauncherCandidates(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(filepath.Join(home, "tools"), 0o755)
	sep := string(os.PathListSeparator)
	pathEnv := strings.Join([]string{
		"/usr/bin",
		filepath.Join(home, "go", "bin"), // preferred: kept although missing
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "bin"), // duplicate
		filepath.Join(home, "tools"),
		filepath.Join(home, "gone"), // non-preferred and missing: dropped
		"",
		"~/bin", // tilde form
	}, sep)
	got := launcherCandidates(home, pathEnv)
	want := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, "tools"),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	if launcherCandidates("", pathEnv) != nil {
		t.Fatal("no home must give no candidates")
	}
}

func TestLauncherDirFlagWritesScript(t *testing.T) {
	home, bin := launcherTestEnv(t)
	dir := filepath.Join(home, "mybin")
	var out, errb bytes.Buffer
	if rc := runLauncher([]string{"--dir", dir}, strings.NewReader(""), &out, &errb); rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, errb.String())
	}
	path := filepath.Join(dir, launcherName())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), bin) || !strings.Contains(string(data), launcherMarker) {
		t.Fatalf("script:\n%s", data)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode()&0o111 == 0 {
			t.Fatalf("not executable: %v", st.Mode())
		}
	}
	if !strings.Contains(out.String(), "not on your PATH yet") {
		t.Fatalf("expected PATH hint, got %s", out.String())
	}
}

func TestLauncherRepointsOwnScriptRefusesForeign(t *testing.T) {
	home, _ := launcherTestEnv(t)
	dir := filepath.Join(home, "bin")
	if rc := runLauncher([]string{"--dir", dir}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); rc != 0 {
		t.Fatal("first install failed")
	}
	// A rebuilt binary elsewhere: rerun repoints without --force.
	other := filepath.Join(t.TempDir(), "erbrus")
	os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755)
	launcherExecutable = func() (string, error) { return other, nil }
	if rc := runLauncher([]string{"--dir", dir}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); rc != 0 {
		t.Fatal("repoint failed")
	}
	data, _ := os.ReadFile(filepath.Join(dir, launcherName()))
	if !strings.Contains(string(data), other) {
		t.Fatalf("not repointed:\n%s", data)
	}
	// A foreign file is refused unless --force.
	path := filepath.Join(dir, launcherName())
	os.WriteFile(path, []byte("real binary here"), 0o755)
	var errb bytes.Buffer
	if rc := runLauncher([]string{"--dir", dir}, strings.NewReader(""), &bytes.Buffer{}, &errb); rc == 0 || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("rc=%d stderr=%s", rc, errb.String())
	}
	if rc := runLauncher([]string{"--dir", dir, "--force"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); rc != 0 {
		t.Fatal("--force failed")
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), launcherMarker) {
		t.Fatal("--force did not replace the file")
	}
}

func TestLauncherPromptOffersPathDirsUnderHome(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".local", "bin")
	gobin := filepath.Join(home, "go", "bin")
	os.MkdirAll(gobin, 0o755)
	// launcherTestEnv makes its own home; override after so the PATH refers
	// to this one.
	_, _ = launcherTestEnv(t, "/usr/bin", gobin, local)
	launcherHomeDir = func() (string, error) { return home, nil }

	// Enter picks the first (preferred ~/.local/bin, created on demand).
	var out bytes.Buffer
	if rc := runLauncher(nil, strings.NewReader("\n"), &out, &bytes.Buffer{}); rc != 0 {
		t.Fatalf("rc=%d out=%s", rc, out.String())
	}
	if _, err := os.Stat(filepath.Join(local, launcherName())); err != nil {
		t.Fatalf("enter did not install into %s: %v\n%s", local, err, out.String())
	}
	if !strings.Contains(out.String(), "1. "+local) || !strings.Contains(out.String(), "(will be created)") || !strings.Contains(out.String(), "2. "+gobin) {
		t.Fatalf("menu:\n%s", out.String())
	}
	if strings.Contains(out.String(), "not on your PATH yet") {
		t.Fatalf("dir on PATH must not get the hint:\n%s", out.String())
	}

	// A number picks that entry.
	if rc := runLauncher(nil, strings.NewReader("2\n"), &bytes.Buffer{}, &bytes.Buffer{}); rc != 0 {
		t.Fatal("pick 2 failed")
	}
	if _, err := os.Stat(filepath.Join(gobin, launcherName())); err != nil {
		t.Fatal("2 did not install into go/bin")
	}

	// Anything else is a path (~ expands); q cancels; a bad number fails.
	if rc := runLauncher(nil, strings.NewReader("~/custom\n"), &bytes.Buffer{}, &bytes.Buffer{}); rc != 0 {
		t.Fatal("custom path failed")
	}
	if _, err := os.Stat(filepath.Join(home, "custom", launcherName())); err != nil {
		t.Fatal("custom path not honoured")
	}
	var errb bytes.Buffer
	if rc := runLauncher(nil, strings.NewReader("q\n"), &bytes.Buffer{}, &errb); rc == 0 || !strings.Contains(errb.String(), "cancelled") {
		t.Fatalf("q: rc=%d %s", rc, errb.String())
	}
	if rc := runLauncher(nil, strings.NewReader("9\n"), &bytes.Buffer{}, &bytes.Buffer{}); rc == 0 {
		t.Fatal("bad number accepted")
	}
}

func TestLauncherPromptAsksWhenNothingUnderHome(t *testing.T) {
	home, _ := launcherTestEnv(t, "/usr/bin", "/usr/local/bin")
	var out bytes.Buffer
	if rc := runLauncher(nil, strings.NewReader("\n"), &out, &bytes.Buffer{}); rc != 0 {
		t.Fatalf("rc=%d out=%s", rc, out.String())
	}
	def := filepath.Join(home, ".local", "bin")
	if !strings.Contains(out.String(), "No directory under your home is on PATH") || !strings.Contains(out.String(), "["+def+"]") {
		t.Fatalf("prompt:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(def, launcherName())); err != nil {
		t.Fatal("default ~/.local/bin not used")
	}
	if !strings.Contains(out.String(), "not on your PATH yet") {
		t.Fatal("expected PATH hint")
	}
	// A typed directory wins over the default.
	custom := filepath.Join(home, "tools")
	if rc := runLauncher(nil, strings.NewReader(custom+"\n"), &bytes.Buffer{}, &bytes.Buffer{}); rc != 0 {
		t.Fatal("typed dir failed")
	}
	if _, err := os.Stat(filepath.Join(custom, launcherName())); err != nil {
		t.Fatal("typed dir not used")
	}
}

func TestAgentsWarnWhenErbrusNotOnPath(t *testing.T) {
	agentsTestHome(t) // lookPath finds nothing, erbrus included
	_, bin := launcherTestEnv(t)
	var out, errb bytes.Buffer
	if rc := runAgents([]string{"list"}, strings.NewReader(""), &out, &errb); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(errb.String(), "not on your PATH") || !strings.Contains(errb.String(), bin+" launcher") {
		t.Fatalf("stderr:\n%s", errb.String())
	}
	// Found on PATH: silent.
	agentsLookPath = func(name string) (string, error) {
		if name == "erbrus" {
			return "/usr/local/bin/erbrus", nil
		}
		return "", os.ErrNotExist
	}
	errb.Reset()
	runAgents([]string{"list"}, strings.NewReader(""), &out, &errb)
	if strings.Contains(errb.String(), "not on your PATH") {
		t.Fatalf("unexpected warning:\n%s", errb.String())
	}
}
