// Package preset merges agent presets: global config presets overlaid by a
// project's .erbrus.yaml presets. A preset only prefills a spawn request;
// every field stays overridable at spawn time.
package preset

import (
	"fmt"
	"sort"
	"strings"

	"erbrus/internal/config"
)

func Merge(global, project map[string]config.Preset) map[string]config.Preset {
	m := make(map[string]config.Preset, len(global)+len(project))
	for k, v := range global {
		m[k] = v
	}
	for k, v := range project {
		m[k] = v
	}
	return m
}

func Resolve(name string, merged map[string]config.Preset) (config.Preset, error) {
	if p, ok := merged[name]; ok {
		return p, nil
	}
	names := make([]string, 0, len(merged))
	for k := range merged {
		names = append(names, k)
	}
	sort.Strings(names)
	return config.Preset{}, fmt.Errorf("unknown preset %q (available: %s)", name, strings.Join(names, ", "))
}
