package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStopCommand(t *testing.T) {
	var hit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":7,"agent_name":"antigravity","status":"stopped"}`))
	}))
	defer srv.Close()
	t.Setenv("ERBRUS_URL", srv.URL)

	var out, errb bytes.Buffer
	if rc := Run([]string{"stop", "7"}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d %s", rc, errb.String())
	}
	if hit != "POST /api/runs/7/stop" {
		t.Fatalf("hit %q", hit)
	}
	if !strings.Contains(out.String(), "stopped antigravity (run 7)") {
		t.Fatalf("out: %s", out.String())
	}
	if rc := Run([]string{"stop", "x"}, &out, &errb); rc != 2 {
		t.Fatalf("bad id rc=%d", rc)
	}
	if rc := Run([]string{"stop"}, &out, &errb); rc != 2 {
		t.Fatalf("no id rc=%d", rc)
	}
}
