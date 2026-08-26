package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"erbrus/internal/client"
	"erbrus/internal/config"
	"erbrus/internal/wt"
)

const scaffoldYAML = `# erbrus per-repo settings. Everything is optional; global config fills gaps.
# session: erbrus-myproject
# attach_session: ""
# channel_per_worktree: true
# presets:
#   claude:
#     provider: claude-code
`

// runInit registers the repo containing the cwd as a project (auto-configure)
// and scaffolds .erbrus.yaml. Spawning is Plan 2; init never spawns.
func runInit(args []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root, err := wt.MainRoot(wt.ExecRunner, cwd)
	if err != nil {
		fmt.Fprintln(stderr, "not inside a git repository:", err)
		return 1
	}

	c, _, _ := client.FromEnv()
	p, err := c.AddProject(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if p.Created {
		fmt.Fprintf(stdout, "created project %s (%s)\n", p.Name, p.RepoPath)
	} else {
		fmt.Fprintf(stdout, "project %s already registered (%s)\n", p.Name, p.RepoPath)
	}
	for _, ch := range p.Channels {
		fmt.Fprintf(stdout, "  channel #%s (id %d)\n", ch.Name, ch.ID)
	}
	if p.Warning != "" {
		fmt.Fprintln(stdout, "warning:", p.Warning)
	}

	scaffold := config.RepoConfigPath(root)
	if _, err := os.Stat(scaffold); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(scaffold), 0o755); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := os.WriteFile(scaffold, []byte(scaffoldYAML), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "scaffolded %s\n", scaffold)
	}
	if _, err := os.Stat(filepath.Join(root, ".erbrus.yaml")); err == nil {
		fmt.Fprintf(stdout, "note: legacy %s/.erbrus.yaml still read as fallback; move settings to %s\n", root, scaffold)
	}
	return 0
}
