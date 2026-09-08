package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erbrus/internal/server"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// Set by msgTestServer so tests can reach the server and store behind the
// URL (SetRuntime, SetConfigPath, run rows).
var (
	lastTestServer *server.Server
	lastTestStore  *store.Store
)

func TestProviderSetShowAndScreen(t *testing.T) {
	url, ch := msgTestServer(t)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("port: 7420\n"), 0o644)
	lastTestServer.SetConfigPath(cfgPath)
	fs := &cliFakeSpawner{raw: "hello\n❯\n"}
	lastTestServer.SetRuntime(fs, "/abs/erbrus", url)
	run, _ := lastTestStore.CreateRun(store.AgentRun{ChannelID: ch, Provider: "newcli", AgentName: "n", Workdir: "/w", Status: "running", Spawner: "tmux", TmuxTarget: "s:1"})
	t.Setenv("ERBRUS_URL", url)
	t.Setenv("ERBRUS_TOKEN", "")
	t.Setenv("ERBRUS_RUN_ID", "")

	var out, errOut bytes.Buffer
	code := Run([]string{"provider", "set", "newcli", "--command", `newcli --model {model} {args} "{prompt}"`, "--prompt", "arg", "--waiting", `^❯\s*$`}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "saved provider newcli") {
		t.Fatalf("set: %d %s %s", code, out.String(), errOut.String())
	}
	if doc, _ := os.ReadFile(cfgPath); !strings.Contains(string(doc), "  newcli:") {
		t.Fatalf("config not written:\n%s", doc)
	}
	out.Reset()
	if code := Run([]string{"provider", "show", "newcli"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "command: newcli") || !strings.Contains(out.String(), "config override") {
		t.Fatalf("show: %d %s", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"screen", "capture", "--run", fmt.Sprint(run.ID)}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "state: waiting\n") || !strings.Contains(out.String(), "hello") {
		t.Fatalf("capture: %d %s %s", code, out.String(), errOut.String())
	}
	// ERBRUS_RUN_ID stands in for --run (an agent looking at itself).
	out.Reset()
	t.Setenv("ERBRUS_RUN_ID", fmt.Sprint(run.ID))
	if code := Run([]string{"screen", "capture", "--lines", "3"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "last 2 lines") {
		t.Fatalf("capture via env: %d %s %s", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Run([]string{"screen", "test", "--provider", "x", "--run", fmt.Sprint(run.ID), "--question", "hello"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "state: question") || !strings.Contains(out.String(), "[question] hello") {
		t.Fatalf("test: %d %s %s", code, out.String(), errOut.String())
	}
	if code := Run([]string{"screen", "test", "--provider", "x", "--run", fmt.Sprint(run.ID), "--working", "("}, &out, &errOut); code == 0 {
		t.Fatal("bad pattern must fail")
	}
	out.Reset()
	if code := Run([]string{"provider", "set", "newcli", "--clear-rules"}, &out, &errOut); code != 0 {
		t.Fatalf("clear: %s", errOut.String())
	}
	out.Reset()
	Run([]string{"provider", "show", "newcli"}, &out, &errOut)
	if !strings.Contains(out.String(), "built-in") {
		t.Fatalf("rules not cleared: %s", out.String())
	}
	if code := Run([]string{"provider", "set", "newcli"}, &out, &errOut); code != 2 {
		t.Errorf("nothing to set must exit 2, got %d", code)
	}
}

// cliFakeSpawner: enough Spawner for screen capture in CLI tests.
type cliFakeSpawner struct{ raw string }

func (f *cliFakeSpawner) Spawn(spawn.RunSpec) (spawn.Handle, error) { return "s:1", nil }
func (f *cliFakeSpawner) Stop(spawn.Handle) error                   { return nil }
func (f *cliFakeSpawner) Alive(spawn.Handle) (bool, error)          { return true, nil }
func (f *cliFakeSpawner) Send(spawn.Handle, string) error           { return nil }
func (f *cliFakeSpawner) SendKeys(spawn.Handle, string) error       { return nil }
func (f *cliFakeSpawner) Capture(spawn.Handle) (spawn.Screen, error) {
	return spawn.Screen{Raw: f.raw, Cols: 80, Rows: 24, Activity: time.Now()}, nil
}
