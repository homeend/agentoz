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
	_, top, err := parseDoc(doc)
	if err != nil {
		return nil, err
	}
	providers := mapValue(top, "providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: no providers section")
	}
	if p := mapValue(providers, provider); p == nil || p.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: provider %q not found", provider)
	}
	return SetProvider(doc, provider, ProviderPatch{Working: List(working), Waiting: List(waiting), Question: List(question)})
}

// ProviderPatch is a partial provider update: nil means keep, an empty
// list or string clears the key.
type ProviderPatch struct {
	Command, DefaultModel, Prompt, Type *string
	Working, Waiting, Question          *[]string
}

func Str(s string) *string      { return &s }
func List(l []string) *[]string { return &l }

// SetProvider rewrites one provider inside config.yaml, keeping every
// other key and comment (yaml.v3 node editing). A missing providers:
// section or entry is created, so a fresh config works too.
func SetProvider(doc []byte, name string, p ProviderPatch) ([]byte, error) {
	root, top, err := parseDoc(doc)
	if err != nil {
		return nil, err
	}
	providers := mapValue(top, "providers")
	if providers == nil {
		providers = &yaml.Node{Kind: yaml.MappingNode}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "providers"}, providers)
	} else if providers.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: providers is not a mapping")
	}
	entry := mapValue(providers, name)
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode}
		providers.Content = append(providers.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, entry)
	} else if entry.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config.yaml: provider %q is not a mapping", name)
	}
	for _, kv := range []struct {
		key string
		val *string
	}{{"command", p.Command}, {"default_model", p.DefaultModel}, {"prompt", p.Prompt}, {"type", p.Type}} {
		if kv.val != nil {
			setScalar(entry, kv.key, *kv.val)
		}
	}
	for _, kv := range []struct {
		key  string
		list *[]string
	}{{"screen_working", p.Working}, {"screen_waiting", p.Waiting}, {"screen_question", p.Question}} {
		if kv.list != nil {
			setSeq(entry, kv.key, *kv.list)
		}
	}
	return encodeDoc(root)
}

// parseDoc parses config.yaml into nodes; an empty document becomes an
// empty top-level mapping.
func parseDoc(doc []byte) (root *yaml.Node, top *yaml.Node, err error) {
	root = &yaml.Node{}
	if err := yaml.Unmarshal(doc, root); err != nil {
		return nil, nil, fmt.Errorf("config.yaml: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		root = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	top = root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("config.yaml: top level is not a mapping")
	}
	return root, top, nil
}

func encodeDoc(root *yaml.Node) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	enc.Close()
	return out.Bytes(), nil
}

// setScalar sets key to a plain scalar, or removes it when v is empty.
func setScalar(m *yaml.Node, key, v string) {
	idx := -1
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			idx = i
			break
		}
	}
	if v == "" {
		if idx >= 0 {
			m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
		}
		return
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	if idx >= 0 {
		m.Content[idx+1] = n
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, n)
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
