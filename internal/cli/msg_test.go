package cli

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"erbrus/internal/config"
	"erbrus/internal/server"
	"erbrus/internal/store"
)

func msgTestServer(t *testing.T) (string, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	run := func(dir, name string, args ...string) ([]byte, error) { return nil, fmt.Errorf("no") }
	ts := httptest.NewServer(server.New(st, cfg, run).Handler())
	t.Cleanup(ts.Close)
	p, _ := st.CreateProject("x", t.TempDir())
	c, _ := st.CreateChannel(p.ID, "general", "", "")
	return ts.URL, c.ID
}

func TestMsgSendAndRead(t *testing.T) {
	url, ch := msgTestServer(t)
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_CHANNEL", fmt.Sprint(ch))
	t.Setenv("ERBRUS_TOKEN", "")

	var out, errOut bytes.Buffer
	code := Run([]string{"msg", "send", "hello world"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("send exit = %d, stderr: %s", code, errOut.String())
	}

	out.Reset()
	code = Run([]string{"msg", "read"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("read exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hello world") {
		t.Errorf("read output %q", out.String())
	}
	if !strings.Contains(out.String(), "you") {
		t.Errorf("author missing in %q", out.String())
	}
}

func TestMsgSendReportFlag(t *testing.T) {
	url, ch := msgTestServer(t)
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_CHANNEL", fmt.Sprint(ch))

	var out, errOut bytes.Buffer
	if code := Run([]string{"msg", "send", "--report", "findings"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	out.Reset()
	Run([]string{"msg", "read"}, &out, &errOut)
	if !strings.Contains(out.String(), "(report)") {
		t.Errorf("read output %q, want kind report shown", out.String())
	}
}

func TestMsgSendNoChannel(t *testing.T) {
	t.Setenv("ERBRUS_URL", "http://127.0.0.1:1")
	t.Setenv("ERBRUS_CHANNEL", "")
	var out, errOut bytes.Buffer
	if code := Run([]string{"msg", "send", "x"}, &out, &errOut); code == 0 {
		t.Fatal("want non-zero exit without channel")
	}
	if !strings.Contains(errOut.String(), "channel") {
		t.Errorf("stderr %q should mention channel", errOut.String())
	}
}
