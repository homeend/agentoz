package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPresetSetAndShow(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = nil
		if len(b) > 0 {
			json.Unmarshal(b, &body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"agy","provider":"antigravity","model":"m1"}`))
	}))
	defer srv.Close()
	t.Setenv("ERBRUS_URL", srv.URL)

	var out, errb bytes.Buffer
	if rc := Run([]string{"preset", "set", "agy", "--provider", "antigravity", "--model", "m1"}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d %s", rc, errb.String())
	}
	if method != "PUT" || path != "/api/presets/agy" || body["provider"] != "antigravity" || body["model"] != "m1" {
		t.Fatalf("%s %s %v", method, path, body)
	}
	if _, has := body["args"]; has {
		t.Fatal("unset flag must not be sent")
	}
	if !strings.Contains(out.String(), "preset agy") || !strings.Contains(out.String(), "provider: antigravity") || !strings.Contains(out.String(), "model: m1") {
		t.Fatalf("out:\n%s", out.String())
	}
	out.Reset()
	if rc := Run([]string{"preset", "show", "agy"}, &out, &errb); rc != 0 || method != "GET" {
		t.Fatalf("show rc=%d method=%s", rc, method)
	}
	if rc := Run([]string{"preset"}, &out, &errb); rc != 2 {
		t.Fatalf("usage rc=%d", rc)
	}
}
