package screen

import "testing"

// agy 1.1.4 trust dialog, as capture-pane -p prints it (indentation and
// the right-aligned model line included).
const agyTrust = "Accessing workspace:\n" +
	"/tmp/x/agywd\n" +
	"Do you trust the contents of this project?\n" +
	"Antigravity CLI requires permission to read, edit, and execute files here.\n" +
	"> Yes, I trust this folder\n" +
	"  No, exit\n" +
	"  ↑/↓ Navigate · enter Confirm\n" +
	"                                                        Gemini 3.8 Flash · high\n"

func TestCursorOptionsReadsAgyDialog(t *testing.T) {
	got := CursorOptions(agyTrust)
	if len(got) != 2 {
		t.Fatalf("options = %+v", got)
	}
	if got[0].Key != "pick:0" || got[0].Label != "Yes, I trust this folder" || !got[0].Current || !got[0].Pick {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Key != "pick:1" || got[1].Label != "No, exit" || got[1].Current {
		t.Errorf("second = %+v", got[1])
	}
	// Cursor on the second line: the line above is still an option.
	moved := "Do you trust it?\n  Yes, I trust this folder\n> No, exit\n  ↑/↓ Navigate · enter Confirm\n"
	got = CursorOptions(moved)
	if len(got) != 2 || got[0].Label != "Yes, I trust this folder" || got[0].Current || !got[1].Current {
		t.Fatalf("moved = %+v", got)
	}
	// Kimi-style marker and hint.
	kimi := "Trust this folder?\n❯ Yes\n  No\n  ↑↓ navigate · Enter select · Esc exit\n"
	if got = CursorOptions(kimi); len(got) != 2 || got[0].Label != "Yes" || got[1].Label != "No" {
		t.Fatalf("kimi = %+v", got)
	}
	// An input prompt alone is no dialog; neither is a marker with no siblings.
	for _, raw := range []string{"> \n", "some text\n> type here\n\n", "> Yes\nplain paragraph text\n"} {
		if got := CursorOptions(raw); got != nil {
			t.Errorf("%q: options = %+v", raw, got)
		}
	}
}

func TestDialogOptionsPrefersNumbered(t *testing.T) {
	raw := "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n"
	got := DialogOptions(raw, Tail(raw, 15))
	if len(got) != 2 || got[0].Key != "1" || got[0].Pick {
		t.Fatalf("numbered = %+v", got)
	}
	if got = DialogOptions(agyTrust, Tail(agyTrust, 15)); len(got) != 2 || got[0].Key != "pick:0" {
		t.Fatalf("cursor fallback = %+v", got)
	}
}
