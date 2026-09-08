package web

import (
	"strings"
	"testing"
)

func TestVendoredXtermIsEmbedded(t *testing.T) {
	for _, name := range []string{"static/vendor/xterm.js", "static/vendor/xterm.css", "static/vendor/xterm.LICENSE", "static/vendor/VERSIONS"} {
		b, err := FS.ReadFile(name)
		if err != nil || len(b) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	v, _ := FS.ReadFile("static/vendor/VERSIONS")
	if !strings.Contains(string(v), "@xterm/xterm 6.0.0") {
		t.Errorf("VERSIONS = %q", v)
	}
	if !strings.HasPrefix(Asset("vendor/xterm.js"), "/static/vendor/xterm.js?v=") {
		t.Errorf("asset url = %q", Asset("vendor/xterm.js"))
	}
}
