package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SetProviderScreenRules rewrites the screen_working / screen_waiting /
// screen_question lists of one provider inside a global config.yaml,
// keeping every other key and comment intact (yaml.v3 node editing).
// Empty lists remove the key; all three empty means "built-in rules".
// The provider must already exist under providers: — screen rules without
// a command make no sense.
func SetProviderScreenRules(doc []byte, provider string, working, waiting, question []string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("config.yaml: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: top level is not a mapping")
	}
	providers := mapValue(top, "providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: no providers section")
	}
	p := mapValue(providers, provider)
	if p == nil || p.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: provider %q not found", provider)
	}
	for _, kv := range []struct {
		key  string
		list []string
	}{{"screen_working", working}, {"screen_waiting", waiting}, {"screen_question", question}} {
		setSeq(p, kv.key, kv.list)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, err
	}
	enc.Close()
	return out.Bytes(), nil
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setSeq sets key to a flow-style sequence of single-quoted strings, or
// removes the key when list is empty.
func setSeq(m *yaml.Node, key string, list []string) {
	idx := -1
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			idx = i
			break
		}
	}
	if len(list) == 0 {
		if idx >= 0 {
			m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
		}
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, v := range list {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v, Style: yaml.SingleQuotedStyle})
	}
	if idx >= 0 {
		m.Content[idx+1] = seq
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, seq)
}
