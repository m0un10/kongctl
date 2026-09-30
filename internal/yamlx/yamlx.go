// Package yamlx holds the two YAML shapes kongctl deals in.
//
// Canonical is decK's own output form: sigs.k8s.io/yaml over a JSON-shaped
// map, which gives sorted keys, two-space indent and unindented sequences.
// `kongctl compose` therefore emits the same bytes as `deck file merge`.
//
// Sorted is the semantic-diff form: a yaml.v3 node tree with every mapping
// sorted by key and single-line scalar styles normalised, so that reordering
// or requoting a file does not show up as a change.
package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/kong/go-apiops/filebasics"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"
)

// Deserialize parses JSON or YAML into a JSON-shaped map, exactly as decK
// reads a state file (numbers become float64).
func Deserialize(data []byte) (map[string]any, error) {
	return filebasics.Deserialize(data)
}

// Canonical serialises a JSON-shaped map the way `deck file` commands do.
func Canonical(data map[string]any) ([]byte, error) {
	out, err := sigsyaml.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("serialising yaml: %w", err)
	}
	return out, nil
}

// SortKeys sorts every mapping in the node tree by key, strips comments and
// normalises the style of single-line scalars. It mutates n.
func SortKeys(n *yaml.Node) {
	if n == nil {
		return
	}
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			SortKeys(c)
		}
	case yaml.MappingNode:
		type pair struct{ k, v *yaml.Node }
		pairs := make([]pair, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			pairs = append(pairs, pair{n.Content[i], n.Content[i+1]})
		}
		sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].k.Value < pairs[j].k.Value })
		n.Content = n.Content[:0]
		for _, p := range pairs {
			SortKeys(p.k)
			SortKeys(p.v)
			n.Content = append(n.Content, p.k, p.v)
		}
	case yaml.ScalarNode:
		if !bytes.ContainsRune([]byte(n.Value), '\n') {
			// Let the encoder pick the plainest safe style; this is what
			// removes quoting churn from the diff.
			n.Style = 0
		}
	}
}

// Sorted parses YAML, sorts it with SortKeys and re-encodes it with a
// two-space indent. It is the canonical form for semantic comparison.
func Sorted(data []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return []byte{}, nil
	}
	SortKeys(&root)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Encode renders a node tree with a two-space indent.
func Encode(n *yaml.Node) ([]byte, error) {
	if n == nil {
		return nil, errors.New("nil node")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Scalar builds a scalar node with an optional style.
func Scalar(value string, style yaml.Style) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style}
}

// Mapping builds a mapping node from ordered key/value pairs.
func Mapping(kv ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: kv}
}

// Sequence builds a sequence node.
func Sequence(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

// StringMap builds a mapping of string pairs with keys sorted.
func StringMap(m map[string]string) *yaml.Node {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	node := Mapping()
	for _, k := range keys {
		node.Content = append(node.Content, Scalar(k, 0), Scalar(m[k], 0))
	}
	return node
}
