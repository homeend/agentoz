package server

import (
	"fmt"
	"html"
	"html/template"
	"strconv"
	"strings"
)

// ansiToHTML renders a `tmux capture-pane -e` screen as safe HTML for a
// <pre>: SGR styling becomes inline-styled spans, every other escape
// sequence (cursor moves, OSC titles, charset switches) is dropped, and all
// text is HTML-escaped. Whitelist by construction: nothing from the input
// ever lands in an attribute — styles are built only from the fixed
// palette and %02x-formatted numbers.
func ansiToHTML(raw string) template.HTML {
	var out strings.Builder
	var text strings.Builder
	var st sgrState
	open := false // a <span> for st is open

	flush := func() {
		if text.Len() == 0 {
			return
		}
		out.WriteString(html.EscapeString(text.String()))
		text.Reset()
	}
	restyle := func(next sgrState) {
		if next == st {
			return
		}
		flush()
		if open {
			out.WriteString("</span>")
			open = false
		}
		st = next
		if s := st.style(); s != "" {
			out.WriteString(`<span style="` + s + `">`)
			open = true
		}
	}

	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != 0x1b {
			text.WriteByte(c)
			continue
		}
		if i+1 >= len(raw) {
			break
		}
		switch raw[i+1] {
		case '[': // CSI: params, then a final byte 0x40..0x7E
			j := i + 2
			for j < len(raw) && (raw[j] < 0x40 || raw[j] > 0x7e) {
				j++
			}
			if j >= len(raw) {
				i = len(raw)
				break
			}
			if raw[j] == 'm' {
				next := st
				applySGR(&next, raw[i+2:j])
				restyle(next)
			}
			i = j
		case ']': // OSC: until BEL or ESC \
			j := i + 2
			for j < len(raw) && raw[j] != 0x07 && !(raw[j] == 0x1b && j+1 < len(raw) && raw[j+1] == '\\') {
				j++
			}
			if j < len(raw) && raw[j] == 0x1b {
				j++
			}
			i = j
		default:
			// nF sequences (ESC ( B charset switches) carry intermediates
			// 0x20..0x2F before a final byte; everything else is ESC + one.
			j := i + 1
			for j < len(raw) && raw[j] >= 0x20 && raw[j] <= 0x2f {
				j++
			}
			i = j
		}
	}
	flush()
	if open {
		out.WriteString("</span>")
	}
	return template.HTML(out.String())
}

type sgrState struct {
	fg, bg                                string // "#rrggbb" or ""
	bold, dim, italic, underline, reverse bool
}

// Default colors of .screen in app.css, used to realize reverse video.
const screenFg, screenBg = "#d6d8dc", "#0c0d0f"

func (s sgrState) style() string {
	fg, bg := s.fg, s.bg
	if s.reverse {
		if fg == "" {
			fg = screenFg
		}
		if bg == "" {
			bg = screenBg
		}
		fg, bg = bg, fg
	}
	var parts []string
	if fg != "" {
		parts = append(parts, "color:"+fg)
	}
	if bg != "" {
		parts = append(parts, "background:"+bg)
	}
	if s.bold {
		parts = append(parts, "font-weight:bold")
	}
	if s.dim {
		parts = append(parts, "opacity:.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

// applySGR folds one SGR parameter string ("1;31", "38;5;114", "38:2::r:g:b")
// into st. Unknown parameters are ignored; a malformed extended color
// ends processing of that sequence.
func applySGR(st *sgrState, params string) {
	fields := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	if len(fields) == 0 {
		*st = sgrState{}
		return
	}
	ps := make([]int, 0, len(fields))
	for _, f := range fields {
		n, _ := strconv.Atoi(f) // "" and junk read as 0
		ps = append(ps, n)
	}
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		switch {
		case p == 0:
			*st = sgrState{}
		case p == 1:
			st.bold = true
		case p == 2:
			st.dim = true
		case p == 3:
			st.italic = true
		case p == 4:
			st.underline = true
		case p == 7:
			st.reverse = true
		case p == 22:
			st.bold, st.dim = false, false
		case p == 23:
			st.italic = false
		case p == 24:
			st.underline = false
		case p == 27:
			st.reverse = false
		case p >= 30 && p <= 37:
			st.fg = palette16[p-30]
		case p == 39:
			st.fg = ""
		case p >= 40 && p <= 47:
			st.bg = palette16[p-40]
		case p == 49:
			st.bg = ""
		case p >= 90 && p <= 97:
			st.fg = palette16[p-90+8]
		case p >= 100 && p <= 107:
			st.bg = palette16[p-100+8]
		case p == 38 || p == 48:
			color, used := extendedColor(ps[i+1:])
			if used == 0 {
				return
			}
			if p == 38 {
				st.fg = color
			} else {
				st.bg = color
			}
			i += used
		}
	}
}

// extendedColor parses the tail of a 38/48 parameter: "5;n" (256-color) or
// "2;r;g;b" (truecolor). Returns the hex color and how many params it ate.
func extendedColor(rest []int) (string, int) {
	if len(rest) >= 2 && rest[0] == 5 {
		return color256(rest[1]), 2
	}
	if len(rest) >= 4 && rest[0] == 2 {
		return fmt.Sprintf("#%02x%02x%02x", clamp8(rest[1]), clamp8(rest[2]), clamp8(rest[3])), 4
	}
	return "", 0
}

func clamp8(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// palette16: the 8 normal + 8 bright ANSI colors, tuned to the dark UI.
var palette16 = [16]string{
	"#1e1e1e", "#e0685a", "#67c98a", "#e5c07b", "#6ea8ff", "#c678dd", "#56b6c2", "#d6d8dc",
	"#5c6370", "#f07178", "#8ce99a", "#f2d479", "#82b1ff", "#e2a0f5", "#7fdbca", "#ffffff",
}

// color256 maps an xterm 256-color index: 0-15 palette, 16-231 6x6x6 cube,
// 232-255 grayscale ramp.
func color256(n int) string {
	switch {
	case n < 0 || n > 255:
		return ""
	case n < 16:
		return palette16[n]
	case n < 232:
		n -= 16
		lv := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", lv(n/36), lv((n/6)%6), lv(n%6))
	default:
		v := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
}
