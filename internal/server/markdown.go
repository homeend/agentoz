package server

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
)

// mdInlineCap bounds how much of an attached .md file is rendered inline
// in the chat; bigger files just keep their download link.
const mdInlineCap = 64 << 10

// renderMarkdown converts markdown to HTML for the chat. goldmark's
// default is the safety property this relies on: raw HTML in the source is
// dropped, so agent-authored messages cannot inject markup — never enable
// html.WithUnsafe here.
func renderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(src), &buf); err != nil {
		// Fall back to nothing; the caller shows the plain body instead.
		return ""
	}
	return template.HTML(buf.String())
}
