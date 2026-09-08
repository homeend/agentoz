package provider

import "testing"

func TestRenderQuotesModel(t *testing.T) {
	r := Registry{"agy": {Command: `agy --model "{model}" {args} -i "{prompt}"`}, "k": {Command: `k --model {model} "{prompt}"`}}
	got, _ := r.Render("agy", "Gemini 3.6 Flash (High)", "", "hi")
	if got != `agy --model 'Gemini 3.6 Flash (High)' -i 'hi'` {
		t.Errorf("got %q", got)
	}
	got, _ = r.Render("k", "sonnet", "", "hi")
	if got != `k --model 'sonnet' 'hi'` {
		t.Errorf("plain model must be quoted too: %q", got)
	}
}

func TestRenderEmptyPromptDropsPlaceholder(t *testing.T) {
	r := Registry{"k": {Command: `k --model {model} {args} "{prompt}"`}}
	got, _ := r.Render("k", "m", "", "")
	if got != `k --model 'm'` {
		t.Errorf("empty prompt must vanish, got %q", got)
	}
	r2 := Registry{"p": {Command: `p -i "{prompt}"`}}
	if got, _ := r2.Render("p", "", "", ""); got != `p` {
		t.Errorf("empty prompt must drop its flag, got %q", got)
	}
}
