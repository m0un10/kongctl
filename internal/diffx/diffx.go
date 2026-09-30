// Package diffx produces unified diffs and the semantic comparison used by
// `kongctl diff` and the MR note.
package diffx

import (
	"bytes"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/m0un10/kongctl/internal/yamlx"
)

// Unified returns a unified diff of a and b with three lines of context, or
// "" when they are identical.
func Unified(a, b []byte, labelA, labelB string) string {
	if bytes.Equal(a, b) {
		return ""
	}
	ud := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(a)),
		B:        difflib.SplitLines(string(b)),
		FromFile: labelA,
		ToFile:   labelB,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(ud)
	if err != nil {
		return ""
	}
	return text
}

// Semantic sorts both sides' keys and normalises scalar styles before
// diffing, so ordering and quoting churn disappear. It returns the diff text
// ("" when equivalent) and whether the raw bytes were identical.
func Semantic(a, b []byte, labelA, labelB string) (diff string, identical bool, err error) {
	identical = bytes.Equal(a, b)
	sa, err := yamlx.Sorted(a)
	if err != nil {
		return "", identical, err
	}
	sb, err := yamlx.Sorted(b)
	if err != nil {
		return "", identical, err
	}
	return Unified(sa, sb, labelA, labelB), identical, nil
}

// Indent prefixes every line with the given string.
func Indent(text, prefix string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
