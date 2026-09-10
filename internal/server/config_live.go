package server

import (
	"fmt"
	"os"
	"reflect"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

// Runtime config access. Providers, presets, tools, wt_bin and gg_bin change while the
// server runs (PUT handlers, the settings page, config.yaml reload), so
// readers take copies under rulesMu and never touch s.cfg's maps directly.
// Port, data_dir, terminal and the session names are read once at start.

func (s *Server) providersSnapshot() map[string]config.Provider {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	out := make(map[string]config.Provider, len(s.cfg.Providers))
	for k, v := range s.cfg.Providers {
		out[k] = v
	}
	return out
}

func (s *Server) presetsSnapshot() map[string]config.Preset {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	out := make(map[string]config.Preset, len(s.cfg.Presets))
	for k, v := range s.cfg.Presets {
		out[k] = v
	}
	return out
}

func (s *Server) providerCfg(name string) (config.Provider, bool) {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	p, ok := s.cfg.Providers[name]
	return p, ok
}

func (s *Server) presetCfg(name string) (config.Preset, bool) {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	p, ok := s.cfg.Presets[name]
	return p, ok
}

func (s *Server) setPresetCfg(name string, p config.Preset) {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	if s.cfg.Presets == nil {
		s.cfg.Presets = map[string]config.Preset{}
	}
	s.cfg.Presets[name] = p
}

func (s *Server) wtBin() string {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return s.cfg.WtBin
}

func (s *Server) ggBin() string {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return s.cfg.GgBin
}

func (s *Server) toolsSnapshot() map[string]config.Tool {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	out := make(map[string]config.Tool, len(s.cfg.Tools))
	for k, v := range s.cfg.Tools {
		out[k] = v
	}
	return out
}

func (s *Server) toolCfg(name string) (config.Tool, bool) {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	t, ok := s.cfg.Tools[name]
	return t, ok
}

// shellToolOverride replaces tools["shell"] with the driver's ShellTool()
// when tools["shell"] is still exactly the untouched built-in default
// (config.BuiltinTools()["shell"]) — that equality is the "the user did
// not define their own shell tool" test. A user-defined shell tool (any
// other command, or Terminal: false) is never touched.
func shellToolOverride(tools map[string]config.Tool, d spawn.Driver) map[string]config.Tool {
	if d == nil || tools == nil {
		return tools
	}
	if tools["shell"] != config.BuiltinTools()["shell"] {
		return tools
	}
	tools["shell"] = config.BuiltinToolsFor(d.ShellTool())["shell"]
	return tools
}

// applyShellTool re-applies shellToolOverride to the running config —
// called after SetDriver so a driver wired in after New() (the normal
// case: New has no driver yet) still gets its shell tool.
func (s *Server) applyShellTool() {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	s.cfg.Tools = shellToolOverride(s.cfg.Tools, s.driver)
}

// compileOverrides builds the screen-rule override map the way New does:
// only providers with at least one list, invalid regexes fall back to
// built-ins with a note on stderr.
func compileOverrides(providers map[string]config.Provider) map[string]screen.Rules {
	rules := map[string]screen.Rules{}
	for name, p := range providers {
		if len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) == 0 {
			continue
		}
		r, err := screen.Compile(p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erbrus: provider %s: %v (using built-in screen rules)\n", name, err)
			continue
		}
		rules[name] = r
	}
	return rules
}

// ReloadConfig re-reads config.yaml and swaps in its providers, presets
// tools, wt_bin and gg_bin, recompiling the screen-rule overrides. It reports whether
// anything changed; a file that does not parse leaves the running config
// untouched. Keys read only at start (port, data_dir, terminal, session
// names) are reported in restart, never applied.
func (s *Server) ReloadConfig() (changed bool, restart []string, err error) {
	if s.configPath == "" {
		return false, nil, nil
	}
	next, err := config.LoadGlobal(s.configPath)
	if err != nil {
		return false, nil, err
	}
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	cur := s.cfg
	for _, kv := range []struct {
		key      string
		old, new any
	}{
		{"port", cur.Port, next.Port}, {"data_dir", cur.DataDir, next.DataDir}, {"terminal", cur.Terminal, next.Terminal},
		{"session_pattern", cur.SessionPattern, next.SessionPattern}, {"session", cur.Session, next.Session},
	} {
		if kv.old != kv.new {
			restart = append(restart, kv.key)
		}
	}
	// Apply the driver's shell override to the freshly loaded tools before
	// comparing: otherwise a driver whose ShellTool() differs from the
	// posix default would make every reload of an unchanged file look
	// "changed" forever, since s.cfg.Tools always carries the override.
	next.Tools = shellToolOverride(next.Tools, s.driver)
	if reflect.DeepEqual(cur.Providers, next.Providers) && reflect.DeepEqual(cur.Presets, next.Presets) &&
		reflect.DeepEqual(cur.Tools, next.Tools) && cur.WtBin == next.WtBin && cur.GgBin == next.GgBin {
		return false, restart, nil
	}
	s.cfg.Providers, s.cfg.Presets, s.cfg.Tools = next.Providers, next.Presets, next.Tools
	s.cfg.WtBin, s.cfg.GgBin = next.WtBin, next.GgBin
	s.rules = compileOverrides(next.Providers)
	return true, restart, nil
}

// StartConfigWatch polls config.yaml's mtime and size every interval and
// reloads on change (polling: no dependency, and inotify is unreliable on
// WSL mounts). The server's own writes change the file too; those reload
// to an identical config and stay silent.
func (s *Server) StartConfigWatch(interval time.Duration, stop <-chan struct{}) {
	if s.configPath == "" {
		return
	}
	stamp := func() string {
		st, err := os.Stat(s.configPath)
		if err != nil {
			return ""
		}
		return fmt.Sprint(st.ModTime().UnixNano(), ":", st.Size())
	}
	go func() {
		last := stamp()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			now := stamp()
			if now == last {
				continue
			}
			last = now
			changed, restart, err := s.ReloadConfig()
			switch {
			case err != nil:
				fmt.Fprintf(os.Stderr, "erbrus: config.yaml: %v (keeping the previous config)\n", err)
			case changed:
				s.rulesMu.RLock()
				np, ns := len(s.cfg.Providers), len(s.cfg.Presets)
				s.rulesMu.RUnlock()
				fmt.Fprintf(os.Stderr, "erbrus: config.yaml reloaded (%d providers, %d presets)\n", np, ns)
			}
			if len(restart) > 0 {
				fmt.Fprintf(os.Stderr, "erbrus: config.yaml: %v changed — takes effect after a restart\n", restart)
			}
		}
	}()
}
