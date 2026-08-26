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
// an immediately preceding flag token. The prompt is shell-quoted; the
// template's own quotes around {prompt} are replaced by ours.
func (r Registry) Render(name, model, args, prompt string) (string, error) {
	p, ok := r[name]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", name)
	}
	if model == "" {
		model = p.DefaultModel
	}
	vals := map[string]string{"model": model, "args": args, "prompt": ShellQuote(prompt)}

	tokens := strings.Fields(p.Command)
	var out []string
	for _, tok := range tokens {
		key, isPlaceholder := placeholderKey(tok)
		if !isPlaceholder {
			out = append(out, tok)
			continue
		}
		v := vals[key]
		if v == "" {
			// Drop the placeholder; drop a preceding flag too.
			if len(out) > 0 && strings.HasPrefix(out[len(out)-1], "-") {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, v)
	}
	return strings.Join(out, " "), nil
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
