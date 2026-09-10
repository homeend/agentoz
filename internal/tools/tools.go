// Package tools renders the command templates of config `tools:` entries:
// programs the human opens in a channel's directory (spec
// docs/superpowers/specs/2026-09-10-tools-design.md).
package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// Vars are the values a template may reference. Distro is $WSL_DISTRO_NAME
// and GOOS runtime.GOOS, both passed in so tests can pick the platform.
type Vars struct {
	Dir, GgBin string
	Distro     string
	GOOS       string
}

// placeholderRe matches {dir}-style tokens only: lowercase words. Shell
// text like ${SHELL:-bash} does not match (':' and '-'), so it survives.
var placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// Render substitutes {dir}, {windir}, {gg_bin} verbatim; any other token
// is an error. Use RenderShell for shell text and RenderArgv for argv.
func Render(name, template string, v Vars) (string, error) {
	return render(name, template, v, func(s string) string { return s })
}

// RenderShell substitutes with the values single-quoted, for templates
// that become shell text (terminal tools' cmd.sh): a directory with a
// space or a quote stays one word.
func RenderShell(name, template string, v Vars) (string, error) {
	return render(name, template, v, shellQuote)
}

// RenderArgv substitutes inside each already-split argument, so a value
// with spaces or backslashes lands intact in ONE argument — the order
// for GUI tools: Split the template first, then render each element.
func RenderArgv(name string, argv []string, v Vars) ([]string, error) {
	out := make([]string, len(argv))
	for i, a := range argv {
		r, err := Render(name, a, v)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

func render(name, template string, v Vars, quote func(string) string) (string, error) {
	var firstErr error
	out := placeholderRe.ReplaceAllStringFunc(template, func(m string) string {
		switch m {
		case "{dir}":
			return quote(v.Dir)
		case "{windir}":
			return quote(WinPath(v.Dir, v.Distro, v.GOOS))
		case "{gg_bin}":
			return quote(v.GgBin)
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("unknown placeholder %s in tools.%s.command", m, name)
		}
		return m
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// shellQuote wraps s in single quotes for POSIX sh; an embedded single
// quote becomes '\” .
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// WinPath is dir as a Windows program would need it. On Windows builds
// the path already is one. On Linux (WSL) /mnt/<x>/rest is drive X:, and
// any other path is reachable as \\wsl.localhost\<distro>\rest when the
// distro name is known; otherwise the path is returned unchanged.
func WinPath(dir, distro, goos string) string {
	if goos == "windows" {
		return dir
	}
	// Exactly /mnt/<letter> or /mnt/<letter>/…; /mnt/tools/x is no drive.
	isDrive := strings.HasPrefix(dir, "/mnt/") && len(dir) >= 6 && (len(dir) == 6 || dir[6] == '/')
	if isDrive {
		drive := strings.ToUpper(dir[5:6])
		rest := strings.TrimPrefix(dir[6:], "/")
		return drive + `:\` + strings.ReplaceAll(rest, "/", `\`)
	}
	if distro != "" && strings.HasPrefix(dir, "/") {
		return `\\wsl.localhost\` + distro + strings.ReplaceAll(dir, "/", `\`)
	}
	return dir
}

// Split turns a rendered GUI command into argv without a shell, so a
// missing binary surfaces as a start error instead of vanishing inside
// `sh -c`. Single quotes are literal; double quotes take \" and \\.
func Split(command string) ([]string, error) {
	var argv []string
	var cur strings.Builder
	inWord, quote := false, rune(0)
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case quote == '"':
			if c == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
				i++
				cur.WriteRune(rs[i])
			} else if c == '"' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				argv = append(argv, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in command %q", command)
	}
	if inWord {
		argv = append(argv, cur.String())
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return argv, nil
}
