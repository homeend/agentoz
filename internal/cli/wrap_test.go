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
