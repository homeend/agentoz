package spawn

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewDriverPicksTmuxOnLinux(t *testing.T) {
	d, err := NewDriver("", "wezterm", "linux", (&recorder{}).run, nil)
	if err != nil || d.Name() != "tmux" {
		t.Fatalf("driver = %v, %v", d, err)
	}
	if _, ok := d.Viewer(); !ok {
		t.Error("tmux driver must offer a viewer")
	}
	if d.PromptByPaste() {
		t.Error("posix host puts the prompt on the command line")
	}
	if got := d.AgentBin(`/mnt/t/erbrus/bin/erbrus`); got != `/mnt/t/erbrus/bin/erbrus` {
		t.Errorf("AgentBin = %q", got)
	}
	if d.ShellTool() != "${SHELL:-bash}" {
		t.Errorf("ShellTool = %q", d.ShellTool())
	}
	if _, ok := d.OpenTerminal("/x", []string{"bash"}); ok {
		t.Error("tmux opens terminal tools as tool runs, not launches")
	}
}

func TestNewDriverRejectsUnknownTerminal(t *testing.T) {
	if _, err := NewDriver("screen", "", "linux", nil, nil); err == nil || !strings.Contains(err.Error(), `terminal "screen"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestPosixWriteCommand(t *testing.T) {
	dir := t.TempDir()
	p, err := posixHost{}.WriteCommand(dir, "claude --model 'x y'")
	if err != nil || p != filepath.Join(dir, "cmd.sh") {
		t.Fatalf("p = %q, %v", p, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "#!/bin/sh\nexec claude --model 'x y'\n" {
		t.Errorf("cmd.sh = %q", b)
	}
}

func TestWindowsHost(t *testing.T) {
	h := windowsHost{}
	dir := t.TempDir()
	p, err := h.WriteCommand(dir, `claude --model 'sonnet 4' --settings "C:\r\s.json"`)
	if err != nil || p != filepath.Join(dir, "cmd.json") {
		t.Fatalf("p = %q, %v", p, err)
	}
	b, _ := os.ReadFile(p)
	want := "[\"claude\",\"--model\",\"sonnet 4\",\"--settings\",\"C:\\\\r\\\\s.json\"]"
	if strings.TrimSpace(string(b)) != want {
		t.Errorf("cmd.json = %s\nwant %s", b, want)
	}
	if !h.PromptByPaste() {
		t.Error("windows host must paste the prompt")
	}
	if got := h.AgentBin(`T:\others\erbrus\bin\erbrus.exe`); got != "T:/others/erbrus/bin/erbrus.exe" {
		t.Errorf("AgentBin = %q", got)
	}
	if h.ShellTool() != "powershell" {
		t.Errorf("ShellTool = %q", h.ShellTool())
	}
	if _, err := h.WriteCommand(dir, `x "unterminated`); err == nil {
		t.Error("bad quoting must be an error")
	}
}

func TestDriverForWrapsAFakeSpawner(t *testing.T) {
	rec := &recorder{}
	d := DriverFor(NewTmux(rec.run))
	if d.Name() != "tmux" {
		t.Errorf("Name = %q", d.Name())
	}
	if _, ok := d.Viewer(); !ok {
		t.Error("a real tmux keeps its viewer through DriverFor")
	}
	if !reflect.DeepEqual(d.Sweep(), []string(nil)) && len(d.Sweep()) != 0 {
		t.Error("sweep on a recorder must kill nothing")
	}
}
