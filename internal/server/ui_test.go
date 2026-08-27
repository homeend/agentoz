package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRootRedirectsToUI(t *testing.T) {
	ts, _, _ := newTestServer(t)
	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/projects" {
		t.Fatalf("status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestStaticServed(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, p := range []string{"/static/app.css", "/static/app.js"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d", p, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestProjectsPage(t *testing.T) {
	ts, _, root := newTestServer(t)
	postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root}).Body.Close()
	resp, err := http.Get(ts.URL + "/ui/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{"erbrus", "general", "Add project", "/ui/channels/"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestProjectsPageAddForm(t *testing.T) {
	ts, st, root := newTestServer(t)
	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(ts.URL+"/ui/projects", url.Values{"repo_path": {root}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	projects, _ := st.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
