package gitx

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Fake is an in-memory Git for tests. Refs map a name to a tree of
// path -> content; the empty ref name is the index.
type Fake struct {
	Top     string
	Refs    map[string]map[string][]byte
	SHAs    map[string]string
	Bases   map[string]string // "a b" -> merge base sha
	Staged  []string
	Configs map[string]string
}

// NewFake builds an empty fake.
func NewFake() *Fake {
	return &Fake{Top: "/repo", Refs: map[string]map[string][]byte{}, SHAs: map[string]string{}, Bases: map[string]string{}, Configs: map[string]string{}}
}

// AddRef registers a ref with a sha and tree.
func (f *Fake) AddRef(name, sha string, tree map[string][]byte) {
	f.Refs[name] = tree
	f.Refs[sha] = tree
	f.SHAs[name] = sha
	f.SHAs[sha] = sha
}

func (f *Fake) TopLevel(context.Context) (string, error) { return f.Top, nil }

func (f *Fake) RevParse(_ context.Context, rev string) (string, error) {
	if sha, ok := f.SHAs[rev]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("ref %q: %w", rev, ErrNotFound)
}

func (f *Fake) MergeBase(_ context.Context, a, b string) (string, error) {
	if sha, ok := f.Bases[a+" "+b]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("no merge base for %s %s", a, b)
}

func (f *Fake) ReadBlob(_ context.Context, ref, path string) ([]byte, error) {
	tree, ok := f.Refs[ref]
	if !ok {
		return nil, fmt.Errorf("%s:%s: %w", ref, path, ErrNotFound)
	}
	data, ok := tree[path]
	if !ok {
		return nil, fmt.Errorf("%s:%s: %w", ref, path, ErrNotFound)
	}
	return data, nil
}

func (f *Fake) ListTree(_ context.Context, ref, dir string) ([]Entry, error) {
	tree, ok := f.Refs[ref]
	if !ok {
		return nil, fmt.Errorf("%s: %w", ref, ErrNotFound)
	}
	prefix := ""
	if dir != "" && dir != "." {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	seen := map[string]bool{}
	var entries []Entry
	for p := range tree {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, isDir := rest, false
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			name, isDir = rest[:i], true
		}
		if !seen[name] {
			seen[name] = true
			entries = append(entries, Entry{Name: name, IsDir: isDir})
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s:%s: %w", ref, dir, ErrNotFound)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (f *Fake) StagedPaths(context.Context) ([]string, error) { return f.Staged, nil }

func (f *Fake) ConfigSet(_ context.Context, key, value string) error {
	f.Configs[key] = value
	return nil
}
