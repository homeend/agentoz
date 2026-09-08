package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"erbrus/internal/provider"
)

// Injectable for tests.
var (
	launcherExecutable = os.Executable
	launcherHomeDir    = os.UserHomeDir
	launcherGetenv     = os.Getenv
)

// launcherMarker identifies a script written by us, so a rerun may
// overwrite it (repoint) while any other file at that path is refused.
const launcherMarker = "erbrus launcher"

const launcherUsage = `usage: erbrus launcher [--dir DIR] [--force]

Installs a launcher script named erbrus (erbrus.cmd on Windows) that runs
this exact binary, so agents and shells can call plain "erbrus". Without
--dir, directories under your home that are already on PATH are offered;
the first one is the default. --force replaces a file that is not an
erbrus launcher.
`

// launcherPreferred ranks well-known user bin directories (relative to
// home) ahead of whatever else PATH happens to contain.
var launcherPreferred = []string{
	filepath.Join(".local", "bin"),
	"bin",
	filepath.Join("go", "bin"),
	filepath.Join(".cargo", "bin"),
}

func runLauncher(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("launcher", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "directory to install the launcher into")
	force := fs.Bool("force", false, "replace a file that is not an erbrus launcher")
	fs.Usage = func() { fmt.Fprint(stderr, launcherUsage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	bin, err := launcherBinary()
	if err != nil {
		fmt.Fprintln(stderr, "launcher:", err)
		return 1
	}
	home, _ := launcherHomeDir()
	pathEnv := launcherGetenv("PATH")
	target := *dir
	if target == "" {
		target, err = chooseLauncherDir(home, pathEnv, stdin, stdout)
		if err != nil {
			fmt.Fprintln(stderr, "launcher:", err)
			return 1
		}
	}
	target = expandHome(target, home)
	path, err := writeLauncher(target, bin, *force)
	if err != nil {
		fmt.Fprintln(stderr, "launcher:", err)
		return 1
	}
	fmt.Fprintf(stdout, "✓ %s → %s\n", path, bin)
	if !dirOnPath(target, pathEnv) {
		fmt.Fprintf(stdout, "%s is not on your PATH yet; add it to your shell startup:\n", target)
		if runtime.GOOS == "windows" {
			fmt.Fprintf(stdout, "  setx PATH \"%s;%%PATH%%\"\n", target)
		} else {
			fmt.Fprintf(stdout, "  export PATH=\"%s:$PATH\"\n", target)
		}
	}
	return 0
}

// launcherBinary is the absolute, symlink-free path of the running binary.
func launcherBinary() (string, error) {
	bin, err := launcherExecutable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		bin = real
	}
	return filepath.Abs(bin)
}

// launcherCandidates lists the PATH entries that live under home,
// preferred names first (kept even when missing, they get created on
// install), then the other existing ones in PATH order, without
// duplicates. Missing non-preferred entries are tool leftovers, not
// places anyone wants a launcher.
func launcherCandidates(home, pathEnv string) []string {
	if home == "" {
		return nil
	}
	home = filepath.Clean(home)
	rank := func(p string) int {
		rel, _ := filepath.Rel(home, p)
		for i, pref := range launcherPreferred {
			if rel == pref {
				return i
			}
		}
		return len(launcherPreferred)
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range filepath.SplitList(pathEnv) {
		if p == "" {
			continue
		}
		p = filepath.Clean(expandHome(p, home))
		if !strings.HasPrefix(p, home+string(filepath.Separator)) || seen[p] {
			continue
		}
		if rank(p) == len(launcherPreferred) {
			if st, err := os.Stat(p); err != nil || !st.IsDir() {
				continue
			}
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// chooseLauncherDir offers the PATH candidates (enter = first) or, when
// there are none, asks for a directory with ~/.local/bin as the default.
func chooseLauncherDir(home, pathEnv string, stdin io.Reader, stdout io.Writer) (string, error) {
	cands := launcherCandidates(home, pathEnv)
	if len(cands) == 0 {
		if home == "" {
			return "", errors.New("no home directory; use --dir")
		}
		def := filepath.Join(home, ".local", "bin")
		fmt.Fprintln(stdout, "No directory under your home is on PATH.")
		fmt.Fprintf(stdout, "Install the launcher into [%s]: ", def)
		line, _ := readLine(stdin)
		if line == "" {
			return def, nil
		}
		return line, nil
	}
	fmt.Fprintln(stdout, "Directories on your PATH:")
	for i, c := range cands {
		note := ""
		if _, err := os.Stat(c); err != nil {
			note = "  (will be created)"
		}
		fmt.Fprintf(stdout, "  %d. %s%s\n", i+1, c, note)
	}
	fmt.Fprint(stdout, "Install the launcher into [1] / number / path: ")
	line, _ := readLine(stdin)
	switch {
	case line == "":
		return cands[0], nil
	case strings.EqualFold(line, "q"):
		return "", errors.New("cancelled")
	}
	if n, err := strconv.Atoi(line); err == nil {
		if n < 1 || n > len(cands) {
			return "", fmt.Errorf("no candidate %d", n)
		}
		return cands[n-1], nil
	}
	return line, nil
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" && err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return line, nil
}

// writeLauncher writes the script into dir (created if needed) and returns
// its path. A file that is not an erbrus launcher is left alone unless
// force is set.
func writeLauncher(dir, bin string, force bool) (string, error) {
	if dir == "" {
		return "", errors.New("no directory")
	}
	name := "erbrus"
	if runtime.GOOS == "windows" {
		name = "erbrus.cmd"
	}
	path := filepath.Join(dir, name)
	if old, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(old), launcherMarker) && !force {
			return "", fmt.Errorf("%s exists and is not an erbrus launcher; use --force to replace it", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(launcherScript(bin)), 0o755); err != nil {
		return "", err
	}
	// WriteFile keeps the old mode of an existing file.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o755); err != nil {
			return "", err
		}
	}
	return path, nil
}

func launcherScript(bin string) string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\nrem " + launcherMarker + " - runs the binary it was created with; rerun \"erbrus launcher\" to repoint\r\n\"" + bin + "\" %*\r\n"
	}
	return "#!/bin/sh\n# " + launcherMarker + " - runs the binary it was created with; rerun 'erbrus launcher' to repoint\nexec " + provider.ShellQuote(bin) + " \"$@\"\n"
}

func dirOnPath(dir, pathEnv string) bool {
	dir = filepath.Clean(dir)
	for _, p := range filepath.SplitList(pathEnv) {
		if p != "" && filepath.Clean(p) == dir {
			return true
		}
	}
	return false
}

func expandHome(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\")) {
		return filepath.Join(home, p[1:])
	}
	return p
}

// warnNotOnPath tells the user that plain "erbrus" does not resolve, which
// is how spawned agents and the skill call it.
func warnNotOnPath(w io.Writer) {
	if _, err := agentsLookPath("erbrus"); err == nil {
		return
	}
	bin, err := launcherBinary()
	if err != nil {
		bin = "erbrus"
	}
	fmt.Fprintf(w, "warning: erbrus is not on your PATH, so agents cannot call it by name.\n"+
		"  either copy %s into a directory on your PATH,\n"+
		"  or run: %s launcher   (installs a launcher script on your PATH)\n\n", bin, bin)
}
