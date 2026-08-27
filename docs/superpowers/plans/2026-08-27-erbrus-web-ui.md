# erbrus Plan 3/3 — Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `http://127.0.0.1:7420/` becomes the erbrus UI: a live channel view (messages, reports with artifact chips and forward/spawn-from-report actions, composer, agents panel), a projects page, a spawn dialog with cross-project targeting, and a settings page — all server-rendered with SSE-driven refresh.

**Architecture:** `internal/web` holds embedded `html/template` pages, one CSS file, and one small hand-written vanilla-JS file (SSE refresh + nothing else clever). `internal/server` gains `/ui/*` handlers that reuse the existing store and, via extracted core functions, the exact logic the JSON API uses. No new JS framework, no Node, no new Go dependencies.

**Tech Stack:** Go 1.26, existing deps only (chi, modernc sqlite, yaml.v3). **Zero new dependencies.**

**Spec:** /mnt/t/others/erbrus/docs/superpowers/specs/2026-08-26-erbrus-design.md (amended: the UI layer is vanilla JS, not htmx — the spec text is already updated)
**Predecessors:** plans 1–2 (branches `plan1-core`, `plan2-spawn`, head cf2c94c). Wireframes (layout reference only): https://claude.ai/code/artifact/952fd05f-1ad8-4da2-98d7-a763a657db11

## Global Constraints

Standing user rules — these override any default habit:

1. Always develop on a git worktree. **Execution starts by creating a worktree branched FROM `plan2-spawn`** (`git worktree add -b plan3-ui <path> plan2-spawn`); branching from main will not compile. All paths below are relative to that worktree root.
2. After finishing this plan, build and provide the binary's **absolute path** for the user's manual testing.
3. Do not merge to main (or into plan1-core/plan2-spawn); the user merges.
4. Never ask about pushing to remote, and never push.
5. Always reference files with absolute paths when talking to the user.

Pinned decisions (implementers do not revisit):

- **No htmx, no vendored framework.** One hand-written `static/app.js`. The spec has been amended to match.
- **SSE refresh contract:** the channel page's `EventSource('/events')` listens for `message` and `run` events; it parses only `channel_id` from the JSON payload; when it equals the open channel's id it re-fetches the matching partial — `message` → `GET /ui/channels/{id}/stream` swapped into `#messages` innerHTML then scrolled to bottom; `run` → `GET /ui/channels/{id}/runs-panel` swapped into `#runs` innerHTML. Full-partial refetch, never client-side append (ordering/dedup come free from the server).
- **Core-logic extraction is its own refactor-only task** (Task 1): `addProjectCore`, `spawnRunCore`, `forwardCore`, `stopRunCore` with the exact signatures below; all existing tests pass unchanged, zero behavior change. UI tasks consume the cores by name; the JSON handlers become thin wrappers.
- **Settings = raw YAML textareas** (global + per project), validated by `yaml.Unmarshal` into the typed structs BEFORE writing; on parse error re-render with the error inline and do not touch the file; writes are atomic (temp file in the same dir + `os.Rename`); the page banners "changes take effect after restarting erbrus serve". Per-repo settings write to `config.RepoConfigPath(...)` — never the legacy in-repo path.
- **Artifact download endpoint** serves `store.Artifact.Path` ONLY after proving the path sits under `<dataDir>/artifacts/` (`filepath.Rel` with no `..` escape) — artifact rows are written by token-holding agents; a crafted row must not become arbitrary-file read. Content-Disposition attachment.
- **Spawn dialog preset list is rendered for the DEFAULT target project** (the channel the dialog was opened from). Switching the target project in the form does NOT refresh the preset list — free-text provider/model/args fields still allow anything. Accepted v1 limitation, noted in the dialog's help text.
- **UI composer posts text only** (kind message|report). File attachments stay CLI/agent territory (`msg send --file`) in v1.
- **No fg spawning from the web UI** (it would run on the server's tty); the dialog spawns tmux runs only.
- Timestamps render in server-local time via a `localtime` template func (DB strings are UTC).
- Visual style: clean, neutral, compact (system-ui font, light background, the wireframes' three-pane layout). Pixel-fidelity to the sketch style is NOT the goal.
- Template escaping is html/template's job — never `template.HTML` around user content (message bodies, names). Bodies render inside `<div class="body">{{.Body}}</div>` with CSS `white-space: pre-wrap`.

Process constraints: TDD where behavior is new (UI handler tests assert status + key substrings via httptest); the refactor task's net is the whole existing suite. Verify each import in this plan's code is actually used; drop unused ones.

---

### Task 1: Extract core logic from JSON handlers (refactor only, zero behavior change)

**Files:**
- Modify: `internal/server/projects.go`, `internal/server/runs.go`, `internal/server/forward.go`

**Interfaces:**
- Produces (all on `*Server`; `status` is an HTTP status to fail with and `errMsg` its message — `status == 0` means success):

```go
// addProjectCore: everything handleAddProject does after decoding —
// absolutize+ExpandHome, lookup (500 on store error), create with the
// isNameCollision suffix loop, ensureChannels. created reports whether a
// new row was made; warning is ensureChannels' warning string.
func (s *Server) addProjectCore(repoPath string) (p store.Project, warning string, created bool, status int, errMsg string)

// spawnRunCore: everything handleSpawnRun does after decoding. payload is
// runJSON (tmux) or fgJSON (fg) on success with status 0.
func (s *Server) spawnRunCore(req runRequest) (payload any, status int, errMsg string)

// forwardCore: everything handleForward does after decoding.
func (s *Server) forwardCore(msgID, channelID int64) (mj messageJSON, status int, errMsg string)

// stopRunCore: everything handleRunStop does after the id parse.
func (s *Server) stopRunCore(id int64) (rj runJSON, status int, errMsg string)
```

Each JSON handler becomes: decode/parse → core → on `status != 0` `httpError(w, status, errMsg)` → else `writeJSON` with the same status codes as today (200 for add-project, 201 for spawn/forward, 200 for stop). SSE publishes and system messages stay INSIDE the cores (they are behavior, and the UI needs them too).

- [ ] **Step 1: Refactor**

Move handler bodies into the cores mechanically. Success statuses: the core returns `status 0` and the handler supplies today's success code — do not change any code path, message string, publish call, or ordering. `handleAddProject` keeps building `projectJSON` from the core's outputs exactly as today.

- [ ] **Step 2: Verify zero behavior change**

Run: `go test ./... && go vet ./...`
Expected: every existing test passes unchanged. No test file is touched in this task.

- [ ] **Step 3: Commit**

```bash
git add internal/server/
git commit -m "refactor: extract addProject/spawnRun/forward/stopRun cores for UI reuse"
```

---

### Task 2: Artifact download endpoint

**Files:**
- Modify: `internal/server/messages.go` (handler), `internal/server/server.go` (route), `internal/store/store.go` (ArtifactByID)
- Test: `internal/server/server_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces:

```go
// store:
func (s *Store) ArtifactByID(id int64) (Artifact, bool, error)
// route (inside /api): GET /artifacts/{id} — 200 file bytes with
// Content-Disposition attachment; 404 unknown id; 404 when the stored path
// escapes <dataDir>/artifacts/ (path-safety pin); 500 store error.
```

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/store_test.go` (inside or alongside TestMessagesSinceAndArtifacts):

```go
func TestArtifactByID(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, _ := s.CreateChannel(p.ID, "general", "", "")
	m, _ := s.CreateMessage(Message{ChannelID: c.ID, Kind: "report", AuthorKind: "human", Body: "b"})
	a, _ := s.AddArtifact(Artifact{MessageID: m.ID, Filename: "f.md", Path: "/data/f.md", Size: 2})
	got, ok, err := s.ArtifactByID(a.ID)
	if err != nil || !ok || got.Filename != "f.md" {
		t.Fatalf("ArtifactByID: %v %v %+v", err, ok, got)
	}
	if _, ok, _ := s.ArtifactByID(999); ok {
		t.Fatal("unknown id must be ok=false")
	}
}
```

Append to `internal/server/server_test.go`:

```go
func TestArtifactDownload(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", "report")
	w.WriteField("body", "with artifact")
	fw, _ := w.CreateFormFile("file", "dl.md")
	io.WriteString(fw, "download me")
	w.Close()
	mresp, err := http.Post(fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID), w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	m := decode[map[string]any](t, mresp)
	artID := int64(m["artifacts"].([]any)[0].(map[string]any)["id"].(float64))

	dresp, err := http.Get(fmt.Sprintf("%s/api/artifacts/%d", ts.URL, artID))
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", dresp.StatusCode)
	}
	body, _ := io.ReadAll(dresp.Body)
	if string(body) != "download me" {
		t.Errorf("body = %q", body)
	}
	if cd := dresp.Header.Get("Content-Disposition"); !strings.Contains(cd, "dl.md") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// Path-safety: a doctored row pointing outside the artifacts dir is 404.
	msgID := int64(m["id"].(float64))
	bad, err := st.AddArtifact(store.Artifact{MessageID: msgID, Filename: "evil", Path: "/etc/passwd", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	bresp, _ := http.Get(fmt.Sprintf("%s/api/artifacts/%d", ts.URL, bad.ID))
	if bresp.StatusCode != http.StatusNotFound {
		t.Errorf("doctored path status = %d, want 404", bresp.StatusCode)
	}
	bresp.Body.Close()

	nresp, _ := http.Get(ts.URL + "/api/artifacts/424242")
	if nresp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown artifact status = %d, want 404", nresp.StatusCode)
	}
	nresp.Body.Close()
}
```

(`strings` may already be imported; verify imports.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run ArtifactByID -v && go test ./internal/server/ -run ArtifactDownload -v`
Expected: FAIL — undefined method / 404 route missing... note the download test will 404 on the route (which is also the wanted failure signature).

- [ ] **Step 3: Implement**

`internal/store/store.go`:

```go
// ArtifactByID fetches one artifact row.
func (s *Store) ArtifactByID(id int64) (Artifact, bool, error) {
	var a Artifact
	var ts string
	err := s.db.QueryRow(`SELECT id, message_id, filename, path, size, created_at
		FROM artifacts WHERE id = ?`, id).
		Scan(&a.ID, &a.MessageID, &a.Filename, &a.Path, &a.Size, &ts)
	if err == sql.ErrNoRows {
		return a, false, nil
	}
	a.CreatedAt = parseTime(ts)
	return a, err == nil, err
}
```

`internal/server/messages.go`:

```go
func (s *Server) handleArtifactDownload(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	a, ok, err := s.st.ArtifactByID(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "artifact not found")
		return
	}
	base := filepath.Join(s.dataDir, "artifacts")
	rel, err := filepath.Rel(base, a.Path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		httpError(w, http.StatusNotFound, "artifact not found")
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.Filename))
	http.ServeFile(w, r, a.Path)
}
```

Route in server.go's /api block: `r.Get("/artifacts/{id}", s.handleArtifactDownload)`.

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/ internal/store/
git commit -m "feat: artifact download endpoint with path-safety check"
```

---

### Task 3: Web scaffold + projects page

**Files:**
- Create: `internal/web/web.go`, `internal/web/templates/layout.html`, `internal/web/templates/projects.html`, `internal/web/static/app.css`, `internal/web/static/app.js` (SSE logic arrives in Task 5 — create it now with only a comment header so the script tag never 404s)
- Create: `internal/server/ui.go`
- Modify: `internal/server/server.go` (routes, template init)
- Test: `internal/server/ui_test.go`

**Interfaces:**
- Produces:

```go
// package web
//go:embed templates/* static/*
var FS embed.FS
// Static returns the embedded static file system rooted at static/.
func Static() http.Handler
// Pages parses each page template with the layout + shared partials.
// Keys: "projects", "channel", "spawn", "forward", "settings" (later tasks
// add their files; Pages must tolerate only-some-pages-existing by globbing).
func Pages(funcs template.FuncMap) map[string]*template.Template

// server: Server gains `pages map[string]*template.Template` (built in New
// via web.Pages(uiFuncs()) — New's signature is unchanged).
func (s *Server) render(w http.ResponseWriter, page string, data any)
// Routes (in Handler(), outside /api):
//   GET /                -> 302 /ui/projects
//   /static/*            -> web.Static()
//   GET  /ui/projects    -> projects page
//   POST /ui/projects    -> form field repo_path -> addProjectCore -> 302 /ui/projects (errors re-render the page with .Error)
// uiFuncs: template.FuncMap{"localtime": localTime}
func localTime(dbTime string) string   // "2006-01-02 15:04:05" UTC -> local "Jan _2 15:04"
```

- [ ] **Step 1: Write the failing tests**

`internal/server/ui_test.go`:

```go
package server

import (
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
```

Add the `readBody` helper (once, in ui_test.go):

```go
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
```

(import `io`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'Root|Static|ProjectsPage' -v`
Expected: FAIL — 404s

- [ ] **Step 3: Implement**

`internal/web/web.go`:

```go
// Package web holds the erbrus UI's embedded templates and static assets.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates/* static/*
var FS embed.FS

func Static() http.Handler {
	sub, _ := fs.Sub(FS, "static")
	return http.FileServer(http.FS(sub))
}

// Pages parses every templates/<name>.html (except layout/partials) with
// the layout and any _*.html partials.
func Pages(funcs template.FuncMap) map[string]*template.Template {
	entries, err := fs.ReadDir(FS, "templates")
	if err != nil {
		panic(err)
	}
	pages := map[string]*template.Template{}
	for _, e := range entries {
		name := e.Name()
		if name == "layout.html" || strings.HasPrefix(name, "_") {
			continue
		}
		key := strings.TrimSuffix(name, ".html")
		files := []string{"templates/layout.html", "templates/" + name}
		if partials, _ := fs.Glob(FS, "templates/_*.html"); len(partials) > 0 {
			files = append(files, partials...)
		}
		pages[key] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(FS, files...))
	}
	return pages
}
```

`internal/web/templates/layout.html`:

```html
<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{block "title" .}}erbrus{{end}}</title>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
{{template "content" .}}
<script src="/static/app.js"></script>
</body>
</html>
```

`internal/web/templates/projects.html`:

```html
{{define "title"}}projects · erbrus{{end}}
{{define "content"}}
<header class="topbar">
  <a class="brand" href="/ui/projects">erbrus</a>
  <span class="crumb">/ projects</span>
  <span class="spacer"></span>
  <a class="btn" href="/ui/settings">Settings</a>
</header>
<main class="page">
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <div class="cards">
    {{range .Projects}}
    <div class="card">
      <h2>{{.Project.Name}}</h2>
      <div class="mono muted">{{.Project.RepoPath}}</div>
      <ul class="chanlist">
        {{range .Channels}}
        <li><a href="/ui/channels/{{.ID}}"># {{.Name}}</a></li>
        {{end}}
      </ul>
    </div>
    {{end}}
    <form class="card addform" method="post" action="/ui/projects">
      <h2>Add project</h2>
      <input class="mono" type="text" name="repo_path" placeholder="/abs/path/to/repo" required>
      <button class="btn primary" type="submit">Add</button>
      <div class="muted small">worktrees are detected and get channels automatically</div>
    </form>
  </div>
</main>
{{end}}
```

`internal/web/static/app.css` (complete file):

```css
* { box-sizing: border-box; }
body { margin: 0; font: 14px/1.5 system-ui, sans-serif; color: #1c1c1c; background: #f6f6f4; }
a { color: #2f6fd6; text-decoration: none; }
a:hover { text-decoration: underline; }
.mono { font-family: ui-monospace, monospace; font-size: 12px; }
.muted { color: #777; }
.small { font-size: 12px; }
.spacer { flex: 1; }
.btn { display: inline-block; padding: 5px 12px; border: 1px solid #bbb; border-radius: 4px; background: #fff; color: #1c1c1c; cursor: pointer; font: inherit; }
.btn:hover { border-color: #888; text-decoration: none; }
.btn.primary { background: #2f6fd6; border-color: #2f6fd6; color: #fff; }
.btn.danger { border-color: #c0392b; color: #c0392b; }
.error { background: #fdecea; border: 1px solid #e5b4ae; color: #86322a; padding: 8px 12px; border-radius: 4px; margin-bottom: 12px; }
.topbar { display: flex; align-items: center; gap: 10px; padding: 10px 18px; background: #fff; border-bottom: 1px solid #ddd; }
.brand { font-weight: 700; font-size: 16px; color: #1c1c1c; }
.crumb { color: #777; }
.page { padding: 18px; max-width: 1100px; margin: 0 auto; }
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; }
.card { background: #fff; border: 1px solid #ddd; border-radius: 6px; padding: 14px; }
.card h2 { margin: 0 0 6px; font-size: 16px; }
.chanlist { list-style: none; margin: 10px 0 0; padding: 0; }
.chanlist li { padding: 2px 0; }
.addform input, .addform textarea, .addform select { width: 100%; margin: 8px 0; padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font: inherit; }
/* channel view */
.shell { display: grid; grid-template-columns: 220px 1fr 280px; height: calc(100vh - 45px); }
.sidebar { background: #fff; border-right: 1px solid #ddd; overflow-y: auto; padding: 10px 0; }
.sidebar .proj { padding: 6px 14px 2px; font-weight: 600; }
.sidebar a.chan { display: block; padding: 3px 14px 3px 24px; color: #444; }
.sidebar a.chan.active { background: #e9eef8; color: #1c1c1c; border-right: 2px solid #2f6fd6; }
.stream { display: flex; flex-direction: column; min-width: 0; }
.chanhead { display: flex; align-items: center; gap: 10px; padding: 10px 16px; background: #fff; border-bottom: 1px solid #ddd; }
.chanhead h1 { margin: 0; font-size: 16px; }
#messages { flex: 1; overflow-y: auto; padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; }
.msg { max-width: 760px; }
.msg .meta { font-size: 12px; color: #777; display: flex; gap: 8px; align-items: baseline; }
.msg .meta .author { font-weight: 600; color: #333; }
.msg .body { white-space: pre-wrap; word-wrap: break-word; }
.msg.system { text-align: center; color: #999; font-size: 12px; }
.msg .tag { font-size: 10px; border: 1px solid #3a9d5d; color: #3a9d5d; border-radius: 8px; padding: 0 6px; }
.msg .actions { margin-top: 3px; display: flex; gap: 8px; }
.msg .actions a { font-size: 12px; }
.artifact { display: inline-block; border: 1px dashed #999; border-radius: 4px; padding: 2px 8px; font-size: 12px; margin-top: 4px; }
.composer { display: flex; gap: 8px; padding: 10px 16px; background: #fff; border-top: 1px solid #ddd; }
.composer textarea { flex: 1; resize: none; height: 40px; padding: 8px; border: 1px solid #ccc; border-radius: 4px; font: inherit; }
.rail { background: #fff; border-left: 1px solid #ddd; overflow-y: auto; padding: 12px; }
.rail h3 { margin: 10px 0 6px; font-size: 11px; letter-spacing: 1px; color: #888; text-transform: uppercase; }
.runcard { border: 1px solid #ddd; border-radius: 4px; padding: 6px 8px; margin-bottom: 6px; font-size: 13px; }
.runcard .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 6px; background: #bbb; }
.runcard.running .dot { background: #3a9d5d; }
.runcard form { display: inline; }
.presetrow { display: flex; justify-content: space-between; align-items: center; padding: 4px 0; }
/* dialogs & settings */
.dialog { max-width: 640px; margin: 24px auto; background: #fff; border: 1px solid #ddd; border-radius: 6px; padding: 18px; }
.dialog h1 { margin: 0 0 12px; font-size: 18px; }
.dialog label { display: block; margin: 10px 0 2px; font-size: 12px; color: #666; letter-spacing: 1px; text-transform: uppercase; }
.dialog input, .dialog select, .dialog textarea { width: 100%; padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font: inherit; }
.dialog textarea { min-height: 100px; }
.dialog .row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.dialog .foot { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
.context { border: 1px dashed #2f6fd6; border-radius: 4px; padding: 8px 10px; font-size: 13px; margin-top: 8px; }
.banner { background: #fff8e1; border: 1px solid #e0c36b; padding: 8px 12px; border-radius: 4px; margin-bottom: 12px; font-size: 13px; }
.settings textarea { width: 100%; min-height: 220px; font-family: ui-monospace, monospace; font-size: 12px; }
```

`internal/web/static/app.js` (this task: header only — Task 5 fills it):

```js
// erbrus UI script. Live-refresh logic lands with the channel view task.
```

`internal/server/ui.go`:

```go
package server

import (
	"net/http"
	"time"

	"erbrus/internal/store"
)

// localTime renders a DB UTC timestamp string in the server's local zone.
func localTime(dbTime string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", dbTime, time.UTC)
	if err != nil {
		return dbTime
	}
	return t.Local().Format("Jan _2 15:04")
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page "+page)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		// headers are gone; best effort
		_ = err
	}
}

type projectCard struct {
	Project  store.Project
	Channels []store.Channel
}

type projectsPage struct {
	Projects []projectCard
	Error    string
}

func (s *Server) projectsPageData(errMsg string) (projectsPage, error) {
	list, err := s.st.Projects()
	if err != nil {
		return projectsPage{}, err
	}
	page := projectsPage{Error: errMsg}
	for _, p := range list {
		chans, err := s.st.ChannelsByProject(p.ID)
		if err != nil {
			return projectsPage{}, err
		}
		page.Projects = append(page.Projects, projectCard{Project: p, Channels: chans})
	}
	return page, nil
}

func (s *Server) handleUIProjects(w http.ResponseWriter, r *http.Request) {
	data, err := s.projectsPageData("")
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, "projects", data)
}

func (s *Server) handleUIAddProject(w http.ResponseWriter, r *http.Request) {
	repoPath := r.FormValue("repo_path")
	if repoPath == "" {
		s.renderProjectsError(w, "repo_path is required")
		return
	}
	if _, _, _, status, errMsg := s.addProjectCore(repoPath); status != 0 {
		s.renderProjectsError(w, errMsg)
		return
	}
	http.Redirect(w, r, "/ui/projects", http.StatusFound)
}

func (s *Server) renderProjectsError(w http.ResponseWriter, msg string) {
	data, err := s.projectsPageData(msg)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.renderNoStatus(w, "projects", data)
}

// renderNoStatus is render without writing a status (already written).
func (s *Server) renderNoStatus(w http.ResponseWriter, page string, data any) {
	if tmpl, ok := s.pages[page]; ok {
		_ = tmpl.ExecuteTemplate(w, "layout.html", data)
	}
}
```

**Correction to the above for the implementer:** `render` must not set Content-Type after a WriteHeader elsewhere — simplest correct split: `render(w, page, data)` sets Content-Type then executes; `renderProjectsError` sets Content-Type, writes 422, then executes via the same template lookup (inline the three lines rather than the awkward `renderNoStatus`; delete `renderNoStatus`).

`internal/server/server.go` — add field `pages map[string]*template.Template`; in `New`: `pages: web.Pages(template.FuncMap{"localtime": localTime}),` (imports `html/template`, `erbrus/internal/web`); in `Handler()` after the /api block:

```go
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/projects", http.StatusFound)
	})
	r.Handle("/static/*", http.StripPrefix("/static/", web.Static()))
	r.Get("/ui/projects", s.handleUIProjects)
	r.Post("/ui/projects", s.handleUIAddProject)
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS (all UI tests plus everything pre-existing)

- [ ] **Step 5: Commit**

```bash
git add internal/web/ internal/server/
git commit -m "feat: web scaffold, layout, and projects page"
```

---

### Task 4: Channel view (page, partials, composer, runs panel)

**Files:**
- Create: `internal/web/templates/channel.html`, `internal/web/templates/_messages.html`, `internal/web/templates/_runs.html`
- Modify: `internal/server/ui.go` (or new `internal/server/ui_channel.go`), `internal/server/server.go` (routes)
- Test: `internal/server/ui_test.go`

**Interfaces:**
- Produces routes:

```
GET  /ui/channels/{id}             — full channel page
GET  /ui/channels/{id}/stream      — the _messages partial only (SSE refresh target)
GET  /ui/channels/{id}/runs-panel  — the _runs partial only
POST /ui/channels/{id}/messages    — form fields kind (message|report), body -> store.CreateMessage as human "you" + hub publish -> 302 back to the channel
POST /ui/runs/{id}/stop            — stopRunCore -> 302 back (form has hidden channel field: back to /ui/channels/{channel})
```

Page data (define in ui_channel.go):

```go
type msgView struct {
	ID        int64
	Kind      string
	Author    string
	Body      string
	When      string        // localtime-rendered
	IsReport  bool
	IsSystem  bool
	Artifacts []store.Artifact
}
type runView struct {
	ID         int64
	AgentName  string
	Provider   string
	Status     string
	TmuxTarget string
	Running    bool
}
type channelPage struct {
	Channel   store.Channel
	Project   store.Project
	Sidebar   []projectCard
	Messages  []msgView
	Runs      []runView
	Presets   []string       // merged preset names for THIS project, sorted
	AttachCmd string         // "tmux attach -t <session>" for this project (session pattern/repo cfg), "" if none derivable
}
```

`_messages.html` renders ONLY the list items (it is the innerHTML of `#messages`); `channel.html` wraps it in `<div id="messages" data-channel="{{.Channel.ID}}">{{template "_messages.html" .}}</div>`. Same shape for `_runs.html` in `<div id="runs">…</div>`. Message rendering: system messages as centered `.msg.system` lines; report messages get the REPORT tag plus action links `Spawn agent from this` → `/ui/spawn?channel={{$.Channel.ID}}&origin={{.ID}}` and `Forward` → `/ui/forward?message={{.ID}}`; artifacts as `.artifact` chips linking `/api/artifacts/{{.ID}}`. Runs panel: each run a `.runcard` (class `running` when Running) showing name/provider/status/tmux target, a Stop form-button for running ones; below, `Presets` as rows with a `Spawn` link to `/ui/spawn?channel={{$.Channel.ID}}&preset={{.}}`; below that the AttachCmd in a `.mono` line when non-empty.

- [ ] **Step 1: Write the failing tests** (append to ui_test.go)

```go
func TestChannelPage(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID),
		map[string]string{"kind": "report", "body": "the report body"}).Body.Close()
	run, _ := st.CreateRun(store.AgentRun{ChannelID: chID, Provider: "codex", AgentName: "impl-x",
		Workdir: root, Status: "starting", Spawner: "tmux"})
	st.StartRun(run.ID, "erbrus-x:1")

	resp2, err := http.Get(fmt.Sprintf("%s/ui/channels/%d", ts.URL, chID))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	for _, want := range []string{
		"the report body", "REPORT", "/ui/spawn?channel=", "/ui/forward?message=",
		"impl-x", "erbrus-x:1", `id="messages"`, `id="runs"`,
		"kimi", // preset from newTestServer cfg appears in the presets rail
	} {
		if !strings.Contains(body, want) {
			t.Errorf("channel page missing %q", want)
		}
	}
}

func TestChannelPartials(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	postJSON(t, fmt.Sprintf("%s/api/channels/%d/messages", ts.URL, chID),
		map[string]string{"kind": "message", "body": "partial body"}).Body.Close()

	sresp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/stream", ts.URL, chID))
	sbody := readBody(t, sresp)
	sresp.Body.Close()
	if !strings.Contains(sbody, "partial body") || strings.Contains(sbody, "<html") {
		t.Errorf("stream partial wrong: %q", sbody[:min(200, len(sbody))])
	}
	rresp, _ := http.Get(fmt.Sprintf("%s/ui/channels/%d/runs-panel", ts.URL, chID))
	rbody := readBody(t, rresp)
	rresp.Body.Close()
	if strings.Contains(rbody, "<html") {
		t.Error("runs partial must not include the layout")
	}
}

func TestChannelComposerPost(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(fmt.Sprintf("%s/ui/channels/%d/messages", ts.URL, chID),
		url.Values{"kind": {"message"}, "body": {"typed in the browser"}})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	msgs, _ := st.MessagesSince(chID, 0, 100)
	found := false
	for _, m := range msgs {
		if m.Body == "typed in the browser" && m.AuthorKind == "human" {
			found = true
		}
	}
	if !found {
		t.Error("composer message not stored")
	}
}

func TestUIStopRun(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "s:9"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)
	rresp := postJSON(t, ts.URL+"/api/runs", map[string]any{"channel_id": chID, "provider": "codex"})
	run := decode[map[string]any](t, rresp)
	runID := int64(run["id"].(float64))

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(fmt.Sprintf("%s/ui/runs/%d/stop", ts.URL, runID),
		url.Values{"channel": {fmt.Sprint(chID)}})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	got, _, _ := st.RunByID(runID)
	if got.Status != "stopped" {
		t.Errorf("status = %q", got.Status)
	}
}
```

(`min` needs Go 1.21+ builtin — fine on 1.26. Verify imports: `net/url`, `fmt`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'ChannelPage|ChannelPartials|Composer|UIStopRun' -v`
Expected: FAIL — 404s

- [ ] **Step 3: Implement**

`internal/web/templates/_messages.html`:

```html
{{define "_messages.html"}}
{{range .Messages}}
{{if .IsSystem}}
<div class="msg system">— {{.Body}} —</div>
{{else}}
<div class="msg">
  <div class="meta">
    <span class="author">{{.Author}}</span>
    {{if .IsReport}}<span class="tag">REPORT</span>{{end}}
    <span>{{.When}}</span>
  </div>
  <div class="body">{{.Body}}</div>
  {{range .Artifacts}}
  <a class="artifact" href="/api/artifacts/{{.ID}}">{{.Filename}} ({{.Size}} B)</a>
  {{end}}
  {{if .IsReport}}
  <div class="actions">
    <a href="/ui/spawn?channel={{$.Channel.ID}}&amp;origin={{.ID}}">Spawn agent from this</a>
    <a href="/ui/forward?message={{.ID}}">Forward…</a>
  </div>
  {{end}}
</div>
{{end}}
{{end}}
{{end}}
```

`internal/web/templates/_runs.html`:

```html
{{define "_runs.html"}}
{{range .Runs}}
<div class="runcard{{if .Running}} running{{end}}">
  <span class="dot"></span><strong>{{.AgentName}}</strong>
  <div class="muted small">{{.Provider}} · {{.Status}}{{if .TmuxTarget}} · {{.TmuxTarget}}{{end}}</div>
  {{if .Running}}
  <form method="post" action="/ui/runs/{{.ID}}/stop">
    <input type="hidden" name="channel" value="{{$.Channel.ID}}">
    <button class="btn danger small" type="submit">Stop</button>
  </form>
  {{end}}
</div>
{{end}}
{{end}}
```

`internal/web/templates/channel.html`:

```html
{{define "title"}}# {{.Channel.Name}} · erbrus{{end}}
{{define "content"}}
<header class="topbar">
  <a class="brand" href="/ui/projects">erbrus</a>
  <span class="crumb">/ {{.Project.Name}} / # {{.Channel.Name}}</span>
  <span class="spacer"></span>
  <a class="btn primary" href="/ui/spawn?channel={{.Channel.ID}}">+ Spawn agent</a>
</header>
<div class="shell">
  <nav class="sidebar">
    {{range .Sidebar}}
    <div class="proj">{{.Project.Name}}</div>
    {{range .Channels}}
    <a class="chan{{if eq .ID $.Channel.ID}} active{{end}}" href="/ui/channels/{{.ID}}"># {{.Name}}</a>
    {{end}}
    {{end}}
  </nav>
  <section class="stream">
    <div id="messages" data-channel="{{.Channel.ID}}">{{template "_messages.html" .}}</div>
    <form class="composer" method="post" action="/ui/channels/{{.Channel.ID}}/messages">
      <textarea name="body" placeholder="Message # {{.Channel.Name}} as human…" required></textarea>
      <select name="kind"><option value="message">message</option><option value="report">report</option></select>
      <button class="btn primary" type="submit">Send</button>
    </form>
  </section>
  <aside class="rail">
    <h3>Agents in channel</h3>
    <div id="runs" data-channel="{{.Channel.ID}}">{{template "_runs.html" .}}</div>
    <h3>Presets</h3>
    {{range .Presets}}
    <div class="presetrow"><span>{{.}}</span><a class="btn" href="/ui/spawn?channel={{$.Channel.ID}}&amp;preset={{.}}">Spawn</a></div>
    {{end}}
    {{if .AttachCmd}}<h3>Attach</h3><div class="mono">{{.AttachCmd}}</div>{{end}}
  </aside>
</div>
{{end}}
```

`internal/server/ui_channel.go` — handlers. Data assembly:

- Load channel (err 500 / missing 404) → project → sidebar via `projectsPageData("")`.Projects → messages via `MessagesSince(chID, 0, 500)` mapped to msgView (`When: localTime(m.CreatedAt.Format("2006-01-02 15:04:05"))` — note store times are already parsed; format back then localtime, or simpler: `When: m.CreatedAt.Local().Format("Jan _2 15:04")` directly since store parsed UTC-naive into time.Time; **pin: use the direct `.Local().Format` route in Go code and keep the `localtime` FuncMap for any template that has only the string**) with artifacts via `ArtifactsByMessage` → runs via `RunsByChannel` mapped (`Running: r.Status == "starting" || r.Status == "running"`) → presets: `repoCfg, _, _ := config.LoadRepo(project.RepoPath)`; merged := preset.Merge(s.cfg.Presets, repoCfg.Presets); sorted keys → AttachCmd: session := repoCfg.Session, else pattern-replaced; if repoCfg.AttachSession != "" use that; `"tmux attach -t " + session`.
- Wait: store.CreateMessage parses DB UTC strings via `parseTime` (no zone) → the time.Time is wall-clock UTC without location; `.Local()` on it would shift wrongly since parseTime used time.Parse (assumes UTC — actually time.Parse with no zone yields UTC). `parseTime` uses `time.Parse(timeFmt, s)` → UTC. So `.Local()` converts correctly. Good — use `m.CreatedAt.Local().Format("Jan _2 15:04")`.
- `handleUIChannelStream` / `handleUIRunsPanel`: build the same channelPage (cheap) and `ExecuteTemplate(w, "_messages.html", data)` / `"_runs.html"` from `s.pages["channel"]`.
- `handleUIComposeMessage`: kind := FormValue (default "message", reject others than message|report with 422), body required; `store.CreateMessage{ChannelID, Kind, AuthorKind: "human", AuthorName: "you", Body}` + `s.hub.Publish("message", s.messageJSON(saved))`; redirect `/ui/channels/{id}`.
- `handleUIStopRun`: `stopRunCore(id)`; on status!=0 → httpError; else redirect to `/ui/channels/` + FormValue("channel").

Routes in Handler():

```go
	r.Get("/ui/channels/{id}", s.handleUIChannel)
	r.Get("/ui/channels/{id}/stream", s.handleUIChannelStream)
	r.Get("/ui/channels/{id}/runs-panel", s.handleUIRunsPanel)
	r.Post("/ui/channels/{id}/messages", s.handleUIComposeMessage)
	r.Post("/ui/runs/{id}/stop", s.handleUIStopRun)
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/ internal/server/
git commit -m "feat: channel view with composer, runs panel, and partials"
```

---

### Task 5: Live refresh (app.js)

**Files:**
- Modify: `internal/web/static/app.js`
- Test: `internal/server/ui_test.go` (one static-content test)

The complete file (this IS the pinned SSE contract — transcribe exactly):

```js
// erbrus UI script: SSE-driven refresh for the channel view.
// Contract: on "message"/"run" events whose channel_id matches the open
// channel, refetch the corresponding partial and swap innerHTML.
(function () {
  var messages = document.getElementById('messages');
  if (!messages) return; // not on a channel page
  var channelID = messages.dataset.channel;
  var runs = document.getElementById('runs');

  function refresh(el, path) {
    fetch(path)
      .then(function (r) { return r.text(); })
      .then(function (html) {
        el.innerHTML = html;
        if (el === messages) el.scrollTop = el.scrollHeight;
      })
      .catch(function () { /* transient; next event retries */ });
  }

  messages.scrollTop = messages.scrollHeight;

  var es = new EventSource('/events');
  es.addEventListener('message', function (e) {
    try {
      var d = JSON.parse(e.data);
      if (String(d.channel_id) === channelID) {
        refresh(messages, '/ui/channels/' + channelID + '/stream');
      }
    } catch (err) { /* ignore malformed */ }
  });
  es.addEventListener('run', function (e) {
    try {
      var d = JSON.parse(e.data);
      if (String(d.channel_id) === channelID && runs) {
        refresh(runs, '/ui/channels/' + channelID + '/runs-panel');
      }
    } catch (err) { /* ignore malformed */ }
  });
})();
```

- [ ] **Step 1: Write the failing test** (append to ui_test.go)

```go
func TestAppJSCarriesSSEContract(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	for _, want := range []string{"EventSource('/events')", "/stream", "/runs-panel", "data.channel", "channel_id"} {
		if !strings.Contains(body, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
}
```

(`data.channel` matches `messages.dataset.channel` — adjust the assertion to `dataset.channel` if needed for exactness; the point is pinning the contract's moving parts.)

- [ ] **Step 2: Verify it fails** (`app.js` is still the one-line header): `go test ./internal/server/ -run AppJS -v` → FAIL

- [ ] **Step 3: Write the file above verbatim; test passes.**

- [ ] **Step 4: Full suite + commit**

```bash
go test ./... && go vet ./...
git add internal/web/ internal/server/
git commit -m "feat: SSE live refresh for channel view"
```

---

### Task 6: Spawn dialog

**Files:**
- Create: `internal/web/templates/spawn.html`, `internal/server/ui_spawn.go`
- Modify: `internal/server/server.go` (routes)
- Test: `internal/server/ui_test.go`

**Interfaces:**

```
GET  /ui/spawn?channel={id}[&preset=NAME][&origin={msgID}]
POST /ui/spawn   (form: target_channel, preset, provider, name, model, args, prompt, workdir, origin_message_id)
     -> spawnRunCore(runRequest{...Fg:false}) -> 302 /ui/channels/{target_channel}
     -> on core error: re-render the dialog with .Error and the submitted values
```

Dialog data:

```go
type channelOption struct {
	ID      int64
	Label   string // "project / # channel"
}
type spawnPage struct {
	Channel   store.Channel  // dialog's default target
	Project   store.Project
	Channels  []channelOption // ALL channels across ALL projects, default first
	Presets   []string        // merged for the DEFAULT project (pinned limitation, help text says so)
	Preset    string          // preselected via ?preset=
	Origin    *msgView        // context banner when ?origin= given (author, excerpt ≤200 chars, artifacts count)
	OriginID  int64
	Error     string
	Form      map[string]string // echo-back on error
}
```

Template (spawn.html): topbar (brand + crumb "spawn agent"), `.dialog` with: context banner when Origin non-nil (`report from {{.Origin.Author}} — attached as context` + excerpt); TARGET CHANNEL select over `.Channels` (selected = default); PRESET select (blank option + names, help text "presets listed for {{.Project.Name}} — changing the target project does not refresh this list"); row: PROVIDER text + NAME text; row: MODEL text + ARGS text; WORKDIR text (placeholder default); PROMPT textarea; hidden origin_message_id; foot: Cancel link back to the channel + primary submit "Spawn agent".

POST handling: parse form; `runRequest{ChannelID: target, Preset, Provider, Name, Model, Args, Prompt, Workdir, OriginMessageID}`; `spawnRunCore`; error → re-render 422 with Error + Form echo; success → redirect to target channel.

- [ ] **Step 1: Write the failing tests** (append to ui_test.go)

```go
func TestSpawnDialogRenders(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	m, _ := st.CreateMessage(store.Message{ChannelID: chID, Kind: "report", AuthorKind: "agent", AuthorName: "impl-x", Body: "origin report body"})

	dresp, _ := http.Get(fmt.Sprintf("%s/ui/spawn?channel=%d&preset=kimi&origin=%d", ts.URL, chID, m.ID))
	body := readBody(t, dresp)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", dresp.StatusCode)
	}
	for _, want := range []string{"target_channel", "kimi", "impl-x", "origin_message_id", "Spawn agent"} {
		if !strings.Contains(body, want) {
			t.Errorf("dialog missing %q", want)
		}
	}
}

func TestSpawnDialogPost(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "ui:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(ts.URL+"/ui/spawn", url.Values{
		"target_channel": {fmt.Sprint(chID)}, "provider": {"codex"}, "prompt": {"from the dialog"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	runs, _ := st.RunsByChannel(chID)
	if len(runs) != 1 || runs[0].Provider != "codex" {
		t.Fatalf("runs = %+v", runs)
	}
	if len(fs.specs) != 1 {
		t.Fatal("spawner not called")
	}
}

func TestSpawnDialogPostErrorRerenders(t *testing.T) {
	ts, _, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chID := int64(p["channels"].([]any)[0].(map[string]any)["id"].(float64))
	fs := &fakeSpawner{handle: "ui:1"}
	testSrv.SetRuntime(fs, "/abs/erbrus", ts.URL)

	resp2, err := http.PostForm(ts.URL+"/ui/spawn", url.Values{
		"target_channel": {fmt.Sprint(chID)}, "provider": {"nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp2.StatusCode)
	}
	if !strings.Contains(body, "nope") {
		t.Error("error page should echo the bad provider")
	}
}
```

- [ ] **Step 2: verify FAIL (404s), Step 3: implement per the interface block, Step 4: full suite green, Step 5: commit**

```bash
git add internal/web/ internal/server/
git commit -m "feat: spawn dialog with cross-project targeting and report context"
```

---

### Task 7: Forward dialog

**Files:**
- Create: `internal/web/templates/forward.html`, `internal/server/ui_forward.go`
- Modify: `internal/server/server.go` (routes)
- Test: `internal/server/ui_test.go`

**Interfaces:**

```
GET  /ui/forward?message={id}  — dialog: source excerpt + target channel select (channelOption list, the source's own channel excluded), submit
POST /ui/forward (form: message_id, target_channel) -> forwardCore -> 302 /ui/channels/{target}
```

- [ ] **Step 1: Failing tests**

```go
func TestForwardDialogAndPost(t *testing.T) {
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	p := decode[map[string]any](t, resp)
	chans := p["channels"].([]any)
	src := int64(chans[0].(map[string]any)["id"].(float64))
	dst := int64(chans[1].(map[string]any)["id"].(float64))
	m, _ := st.CreateMessage(store.Message{ChannelID: src, Kind: "report", AuthorKind: "human", AuthorName: "you", Body: "fwd me"})

	dresp, _ := http.Get(fmt.Sprintf("%s/ui/forward?message=%d", ts.URL, m.ID))
	body := readBody(t, dresp)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK || !strings.Contains(body, "fwd me") || !strings.Contains(body, "target_channel") {
		t.Fatalf("dialog status=%d body missing pieces", dresp.StatusCode)
	}

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := c.PostForm(ts.URL+"/ui/forward", url.Values{
		"message_id": {fmt.Sprint(m.ID)}, "target_channel": {fmt.Sprint(dst)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp2.StatusCode)
	}
	msgs, _ := st.MessagesSince(dst, 0, 100)
	found := false
	for _, mm := range msgs {
		if mm.Body == "fwd me" && mm.OriginMessageID == m.ID {
			found = true
		}
	}
	if !found {
		t.Error("forwarded copy not in target channel")
	}
}
```

- [ ] **Step 2: verify FAIL, Step 3: implement** (data: source msgView + channelOption list minus source channel; POST parses ids → forwardCore → on status!=0 re-render 422 with error → else redirect), **Step 4: full suite, Step 5: commit**

```bash
git add internal/web/ internal/server/
git commit -m "feat: forward dialog"
```

---

### Task 8: Settings page

**Files:**
- Create: `internal/web/templates/settings.html`, `internal/server/ui_settings.go`
- Modify: `internal/server/server.go` (routes; Server needs the global-config PATH — add field `configPath string` set via a new optional setter `SetConfigPath(p string)` called from serve.go with the same `configPath()` value it already computes; empty → the page shows the global section read-only-with-note)
- Modify: `internal/cli/serve.go` (call `srv.SetConfigPath(configPath())`)
- Test: `internal/server/ui_test.go`

**Interfaces:**

```
GET  /ui/settings — banner "changes take effect after restarting erbrus serve";
     Global section: path + textarea (raw file bytes; if missing, a commented starter based on the spec's example);
     one section per project: RepoConfigPath(p.RepoPath) + textarea (raw file or the scaffold template from initcmd).
POST /ui/settings/global          (form: content) — yaml.Unmarshal into config.Global copy of Defaults() first; on error 422 re-render with .Error, file untouched; else atomic write (os.CreateTemp same dir + os.Rename, MkdirAll first) -> 302 /ui/settings
POST /ui/settings/repo?project=N  (form: content) — same with config.Repo and RepoConfigPath
```

- [ ] **Step 1: Failing tests**

```go
func TestSettingsRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ERBRUS_HOME", tmp)
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 7420\n"), 0o644)

	ts, _, root := newTestServer(t)
	testSrv.SetConfigPath(cfgPath)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	resp.Body.Close()

	gresp, _ := http.Get(ts.URL + "/ui/settings")
	body := readBody(t, gresp)
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", gresp.StatusCode)
	}
	for _, want := range []string{"restarting", "port: 7420", ".erbrus.yaml"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := c.PostForm(ts.URL+"/ui/settings/global", url.Values{"content": {"port: 9999\n"}})
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Fatalf("valid save status = %d", r2.StatusCode)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "9999") {
		t.Errorf("global config not written: %q", data)
	}

	r3, _ := http.PostForm(ts.URL+"/ui/settings/global", url.Values{"content": {"port: [broken"}})
	b3 := readBody(t, r3)
	r3.Body.Close()
	if r3.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save status = %d, want 422", r3.StatusCode)
	}
	if !strings.Contains(b3, "yaml") && !strings.Contains(b3, "parse") {
		t.Error("error page should mention the parse failure")
	}
	data, _ = os.ReadFile(cfgPath)
	if strings.Contains(string(data), "broken") {
		t.Error("invalid YAML must not be written")
	}
}

func TestSettingsRepoSave(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ERBRUS_HOME", tmp)
	ts, st, root := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/projects", map[string]string{"repo_path": root})
	resp.Body.Close()
	projects, _ := st.Projects()

	c := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	r2, err := c.PostForm(fmt.Sprintf("%s/ui/settings/repo?project=%d", ts.URL, projects[0].ID),
		url.Values{"content": {"session: from-ui\n"}})
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", r2.StatusCode)
	}
	repoCfg, legacy, err := config.LoadRepo(projects[0].RepoPath)
	if err != nil || legacy || repoCfg.Session != "from-ui" {
		t.Errorf("repo config not written to new location: %+v legacy=%v err=%v", repoCfg, legacy, err)
	}
}
```

(imports: `os`, `path/filepath`, `erbrus/internal/config` — verify.)

- [ ] **Step 2: verify FAIL, Step 3: implement** — atomic write helper:

```go
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".erbrus-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

Validation: global → `g := config.Defaults(); yaml.Unmarshal(content, &g)`; repo → `var r config.Repo; yaml.Unmarshal(content, &r)`. On unmarshal error re-render 422 with the error text and the submitted content preserved in the textarea. `SetConfigPath` is a two-line setter.

- [ ] **Step 4: full suite, Step 5: commit**

```bash
git add internal/web/ internal/server/ internal/cli/
git commit -m "feat: settings page with validated atomic YAML editing"
```

---

### Task 9: Build + handoff (controller step)

- [ ] `go build ./... && go vet ./... && go test -race ./...` — all green.
- [ ] `mkdir -p bin && go build -o bin/erbrus ./cmd/erbrus`; hand the user the absolute path plus:

```bash
/mnt/t/others/<worktree>/bin/erbrus serve
# then open http://127.0.0.1:7420/ — projects page; add a repo; open a channel;
# spawn an agent from the UI; watch messages stream in live; forward a report;
# spawn-from-report into another project; edit settings (restart to apply).
```

Do NOT merge. Do NOT push. Stop and wait for the user's verdict.

---

## Self-review notes (already applied)

- Spec coverage (Web UI section): channel view w/ sidebar+stream+composer+rail ✔ (T4); reports w/ artifact chips + forward + spawn-from-report actions ✔ (T4/T6/T7); projects page + add form ✔ (T3); spawn dialog incl. target project/channel ✔ (T6); settings editing YAML ✔ (T8); attach hint ✔ (T4); SSE push ✔ (T5); artifact download (Plan-1 parked) ✔ (T2); local-time rendering (Plan-1 parked) ✔ (T4).
- Type consistency: `channelOption`/`msgView`/`runView` defined once (T4/T6 share via the same package); cores from T1 consumed by name in T3/T4/T6/T7; `SetConfigPath` wired in serve.go (T8).
- Known simplifications, intentional & pinned: full-partial SSE refresh; presets-per-default-target; no file upload in composer; no fg from web; settings restart-required; spawn dialog re-render echoes raw form values.
