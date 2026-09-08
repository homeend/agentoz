// Package provider renders provider CLI command lines from config
// templates. Adding a provider is configuration, not code.
package provider

import (
	"fmt"
	"strings"

	"erbrus/internal/config"
)

type Registry map[string]config.Provider

// Render substitutes {model}, {args}, {prompt} into the provider's command
// template. Tokens whose placeholder resolves empty are dropped, along with
// an immediately preceding flag token (only if the flag came from the template,
// not from placeholder substitution). {model} and {prompt} are shell-quoted
// at emission (agy model names carry spaces and parentheses); the
// template's own quotes around them are replaced by ours. Emptiness is
// judged on the raw value, so an empty prompt drops rather than rendering
// as ''.
func (r Registry) Render(name, model, args, prompt string) (string, error) {
	p, ok := r[name]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", name)
	}
	if model == "" {
		model = p.DefaultModel
	}
	vals := map[string]string{"model": model, "args": args, "prompt": prompt}
	quoted := map[string]bool{"model": true, "prompt": true}

	tokens := strings.Fields(p.Command)
	type token struct {
		text    string
		literal bool // true if from template, false if from placeholder
	}
	var out []token
	for _, tok := range tokens {
		key, isPlaceholder := placeholderKey(tok)
		if !isPlaceholder {
			out = append(out, token{text: tok, literal: true})
			continue
		}
		v := vals[key]
		if v == "" {
			// Drop the placeholder; drop a preceding literal flag token too.
			if len(out) > 0 && out[len(out)-1].literal && strings.HasPrefix(out[len(out)-1].text, "-") {
				out = out[:len(out)-1]
			}
			continue
		}
		if quoted[key] {
			v = ShellQuote(v)
		}
		out = append(out, token{text: v, literal: false})
	}
	var result []string
	for _, t := range out {
		result = append(result, t.text)
	}
	return strings.Join(result, " "), nil
}

// placeholderKey recognizes {x}, "{x}", '{x}' as placeholder tokens.
func placeholderKey(tok string) (string, bool) {
	t := strings.Trim(tok, `"'`)
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// ShellQuote single-quotes s for POSIX shells.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
