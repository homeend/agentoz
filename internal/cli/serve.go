package cli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"erbrus/internal/config"
	"erbrus/internal/server"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
	"erbrus/internal/wt"
)

// serveAddrHook, when set (tests), receives the bound address.
var serveAddrHook func(addr string)

// configPath honors ERBRUS_CONFIG for tests; defaults to the XDG location.
func configPath() string {
	if p := os.Getenv("ERBRUS_CONFIG"); p != "" {
		return p
	}
	return config.GlobalPath()
}

func runServe(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.LoadGlobal(configPath())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dataDir := cfg.ResolvedDataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	st, err := store.Open(filepath.Join(dataDir, "erbrus.db"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer st.Close()

	srv := server.New(st, cfg, wt.ExecRunner)
	srv.SetConfigPath(configPath())
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// ln is already bound at this point; its address is the base URL.
	srv.SetRuntime(spawn.NewTmux(spawn.ExecCmdRunner), bin, "http://"+ln.Addr().String())
	if err := srv.Reconcile(); err != nil {
		fmt.Fprintln(stderr, "reconcile:", err)
	}

	fmt.Fprintf(stdout, "erbrus serving on http://%s (data: %s)\n", ln.Addr(), dataDir)
	if serveAddrHook != nil {
		serveAddrHook(ln.Addr().String())
	}
	if err := http.Serve(ln, srv.Handler()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
