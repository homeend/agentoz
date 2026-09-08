package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PresetPatch is a partial preset update: nil means keep, "" removes
// the key (a preset without a model uses the provider's default_model).
type PresetPatch struct {
	Provider, Model, Prompt, Args, Name *string
}

// SetPreset rewrites one preset inside config.yaml, keeping every other
// key and comment (yaml.v3 node editing). A missing presets: section or
// entry is created.
func SetPreset(doc []byte, name string, p PresetPatch) ([]byte, error) {
	root, top, err := parseDoc(doc)
	if err != nil {
		return nil, err
	}
	presets := mapValue(top, "presets")
	if presets == nil {
		presets = &yaml.Node{Kind: yaml.MappingNode}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "presets"}, presets)
	} else if presets.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: presets is not a mapping")
	}
	entry := mapValue(presets, name)
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode}
		presets.Content = append(presets.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, entry)
	} else if entry.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: preset %q is not a mapping", name)
	}
	for _, kv := range []struct {
		key string
		val *string
	}{{"provider", p.Provider}, {"model", p.Model}, {"prompt", p.Prompt}, {"args", p.Args}, {"name", p.Name}} {
		if kv.val != nil {
			setScalar(entry, kv.key, *kv.val)
		}
	}
	return encodeDoc(root)
}
