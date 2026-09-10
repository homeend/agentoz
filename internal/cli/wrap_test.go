package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCmdFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cmd.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func wrapEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_TOKEN", "tok")
	t.Setenv("ERBRUS_RUN_ID", "7")
}

func TestWrapReportsZeroExit(t *testing.T) {
	var gotCode = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		gotCode = body["code"]
	}))
	defer srv.Close()
	wrapEnv(t, srv.URL)
	var out, errOut bytes.Buffer
	code := Run([]string{"wrap", writeCmdFile(t, "echo hello; exit 0")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("child stdout not inherited: %q", out.String())
	}
	if gotCode != 0 {
		t.Errorf("reported code = %d", gotCode)
	}
}

func TestWrapPropagatesNonZeroExit(t *testing.T) {
	var gotCode = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		gotCode = body["code"]
	}))
	defer srv.Close()
	wrapEnv(t, srv.URL)
	var out, errOut bytes.Buffer
	code := Run([]string{"wrap", writeCmdFile(t, "exit 3")}, &out, &errOut)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if gotCode != 3 {
		t.Errorf("reported code = %d", gotCode)
	}
}

func TestWrapReportFailureDoesNotMaskExit(t *testing.T) {
	wrapEnv(t, "http://127.0.0.1:1") // nothing listening
	var out, errOut bytes.Buffer
	code := Run([]string{"wrap", writeCmdFile(t, "exit 0")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 despite report failure", code)
	}
	if !strings.Contains(errOut.String(), "report") {
		t.Errorf("stderr should mention the failed report: %q", errOut.String())
	}
}

func TestWrapMissingArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"wrap"}, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestWrapLoadsEnvFileAndRunsArgvJSON(t *testing.T) {
	var gotCode = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runs/42/exit" {
			t.Errorf("exit reported for %s, want run 42 from the env file", r.URL.Path)
		}
		var body map[string]int
		json.NewDecoder(r.Body).Decode(&body)
		gotCode = body["code"]
	}))
	defer srv.Close()
	wrapEnv(t, srv.URL)
	dir := t.TempDir()
	// The env file overrides the inherited ERBRUS_RUN_ID=7 with 42.
	os.WriteFile(filepath.Join(dir, "env"), []byte("ERBRUS_RUN_ID=42\nFROM_FILE=yes\n"), 0o644)
	// argv, no shell: an argument with a space stays one argument.
	os.WriteFile(filepath.Join(dir, "cmd.json"), []byte(`["sh","-c","echo \"$FROM_FILE $1\"","x","a b"]`), 0o644)
	var out, errOut bytes.Buffer
	if code := Run([]string{"wrap", filepath.Join(dir, "cmd.json")}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "yes a b" {
		t.Errorf("stdout = %q", out.String())
	}
	if gotCode != 0 {
		t.Errorf("reported code = %d", gotCode)
	}
}

func TestWrapEnvFileAlsoAppliesToShellScripts(t *testing.T) {
	wrapEnv(t, "http://127.0.0.1:1")
	p := writeCmdFile(t, `echo "$FROM_FILE"`)
	os.WriteFile(filepath.Join(filepath.Dir(p), "env"), []byte("FROM_FILE=sh-too\n"), 0o644)
	var out, errOut bytes.Buffer
	Run([]string{"wrap", p}, &out, &errOut)
	if strings.TrimSpace(out.String()) != "sh-too" {
		t.Errorf("stdout = %q", out.String())
	}
}
