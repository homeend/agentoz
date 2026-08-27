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

// Static returns the embedded static file system rooted at static/.
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
