package tools

import (
	"reflect"
	"testing"
)

func TestWinPath(t *testing.T) {
	cases := []struct{ dir, distro, goos, want string }{
		{"/mnt/t/others/erbrus", "Ubuntu", "linux", `T:\others\erbrus`},
		{"/mnt/c/Users/x/proj", "", "linux", `C:\Users\x\proj`},
		{"/home/u/p", "Ubuntu", "linux", `\\wsl.localhost\Ubuntu\home\u\p`},
		{"/home/u/p", "", "linux", "/home/u/p"},
		{`C:\proj`, "", "windows", `C:\proj`},
		{"/mnt/t", "Ubuntu", "linux", `T:\`},
		{"/mnt/tools/x", "Ubuntu", "linux", `\\wsl.localhost\Ubuntu\mnt\tools\x`},
	}
	for _, c := range cases {
		if got := WinPath(c.dir, c.distro, c.goos); got != c.want {
			t.Errorf("WinPath(%q,%q,%q) = %q, want %q", c.dir, c.distro, c.goos, got, c.want)
		}
	}
}

func TestRender(t *testing.T) {
	v := Vars{Dir: "/mnt/t/p", GgBin: "/opt/gg", Distro: "Ubuntu", GOOS: "linux"}
	for tpl, want := range map[string]string{
		"idea64.exe {windir}":  `idea64.exe T:\p`,
		"code {dir}":           "code /mnt/t/p",
		"{gg_bin} --x":         "/opt/gg --x",
		"${SHELL:-bash}":       "${SHELL:-bash}",
		"echo {dir} > {dir}/o": "echo /mnt/t/p > /mnt/t/p/o",
	} {
		got, err := Render("x", tpl, v)
		if err != nil || got != want {
			t.Errorf("Render(%q) = %q, %v; want %q", tpl, got, err, want)
		}
	}
	if _, err := Render("idea", "idea {project}", v); err == nil || err.Error() != "unknown placeholder {project} in tools.idea.command" {
		t.Fatalf("err = %v", err)
	}
}

// A directory with a space (or a UNC path full of backslashes) must reach
// the program as ONE argument: split the template first, render second.
func TestRenderArgvKeepsValuesWhole(t *testing.T) {
	v := Vars{Dir: "/mnt/c/Users/Jan Kowalski/proj", Distro: "Ubuntu", GOOS: "linux"}
	words, err := Split(`"/mnt/c/Program Files/idea64.exe" {windir} --dir={dir}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderArgv("idea", words, v)
	want := []string{"/mnt/c/Program Files/idea64.exe", `C:\Users\Jan Kowalski\proj`, "--dir=/mnt/c/Users/Jan Kowalski/proj"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderArgv = %v, %v; want %v", got, err, want)
	}
	unc := Vars{Dir: "/home/u/my proj", Distro: "Ubuntu", GOOS: "linux"}
	got, _ = RenderArgv("idea", []string{"idea64.exe", "{windir}"}, unc)
	if !reflect.DeepEqual(got, []string{"idea64.exe", `\\wsl.localhost\Ubuntu\home\u\my proj`}) {
		t.Fatalf("UNC value mangled: %q", got)
	}
	if _, err := RenderArgv("bad", []string{"x", "{nope}"}, v); err == nil {
		t.Fatal("unknown placeholder must fail")
	}
}

// Terminal tool templates become shell text, so values are single-quoted.
func TestRenderShellQuotesValues(t *testing.T) {
	v := Vars{Dir: "/mnt/c/Users/Jan Kowalski/it's", GgBin: "/opt/gg", GOOS: "linux"}
	got, err := RenderShell("gg", "{gg_bin} -C {dir}", v)
	want := `'/opt/gg' -C '/mnt/c/Users/Jan Kowalski/it'\''s'`
	if err != nil || got != want {
		t.Fatalf("RenderShell = %q, %v; want %q", got, err, want)
	}
	if got, _ := RenderShell("shell", "${SHELL:-bash}", v); got != "${SHELL:-bash}" {
		t.Fatalf("shell text must stay untouched: %q", got)
	}
}

func TestSplit(t *testing.T) {
	for in, want := range map[string][]string{
		`idea64.exe T:\p`:                    {"idea64.exe", `T:\p`},
		`"C:\Program Files\x\subl.exe" T:\p`: {`C:\Program Files\x\subl.exe`, `T:\p`},
		`code 'my dir' --new-window`:         {"code", "my dir", "--new-window"},
		`a "q\"q" b`:                         {"a", `q"q`, "b"},
	} {
		got, err := Split(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Split(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{``, `   `, `x "unterminated`} {
		if _, err := Split(bad); err == nil {
			t.Errorf("Split(%q) must fail", bad)
		}
	}
}
