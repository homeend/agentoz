package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// An older erbrus build answers the ping but has no providers API: setup
// must still land the provider in config.yaml and say what happened.
func TestAgentsSetupFallsBackToFileWhenServerRefuses(t *testing.T) {
	agentsTestHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/projects" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("ERBRUS_URL", srv.URL)

	var out, errOut bytes.Buffer
	if code := Run([]string{"agents", "setup", "--agents", "kimi"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	doc, _ := os.ReadFile(os.Getenv("ERBRUS_CONFIG"))
	if !strings.Contains(string(doc), "  kimi:") || !strings.Contains(string(doc), "prompt: paste") {
		t.Fatalf("provider not written to file:\n%s", doc)
	}
	if !strings.Contains(out.String(), "did not accept it") || !strings.Contains(out.String(), "restart erbrus serve with this binary") {
		t.Fatalf("expected refusal note:\n%s", out.String())
	}
	if strings.Contains(out.String(), "applied to the running erbrus") {
		t.Fatal("must not claim a live apply")
	}
}
