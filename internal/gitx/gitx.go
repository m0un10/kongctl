// Package gitx is the thin git layer kongctl needs: resolving refs, reading
// blobs from a ref or the index, and listing trees. It shells out to the git
// binary behind a small interface so that merge-base, worktrees, shallow
// clones and safe.directory all behave exactly as they do for a human, and so
// tests can substitute a fake.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// Git is what the rest of kongctl relies on. ref "" means the index.
type Git interface {
	TopLevel(ctx context.Context) (string, error)
	RevParse(ctx context.Context, rev string) (string, error)
	MergeBase(ctx context.Context, a, b string) (string, error)
	ReadBlob(ctx context.Context, ref, path string) ([]byte, error)
	ListTree(ctx context.Context, ref, dir string) ([]Entry, error)
	StagedPaths(ctx context.Context) ([]string, error)
	ConfigSet(ctx context.Context, key, value string) error
}

// Entry is one directory member.
type Entry struct {
	Name  string
	IsDir bool
}

// ErrNotFound is returned when a ref or path does not exist.
var ErrNotFound = fs.ErrNotExist

// Exec runs git in Dir.
type Exec struct {
	Dir string
}

func (e *Exec) run(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-C", e.Dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// TopLevel returns the working tree root.
func (e *Exec) TopLevel(ctx context.Context) (string, error) {
	out, err := e.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RevParse resolves rev^{commit}; ErrNotFound when it does not exist.
func (e *Exec) RevParse(ctx context.Context, rev string) (string, error) {
	out, err := e.run(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("ref %q: %w", rev, ErrNotFound)
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeBase returns the merge base of a and b.
func (e *Exec) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := e.run(ctx, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ReadBlob returns ref:path, or :path from the index when ref is empty.
func (e *Exec) ReadBlob(ctx context.Context, ref, path string) ([]byte, error) {
	spec := ref + ":" + path
	if _, err := e.run(ctx, "cat-file", "-e", spec); err != nil {
		return nil, fmt.Errorf("%s: %w", spec, ErrNotFound)
	}
	return e.run(ctx, "show", spec)
}

// ListTree lists the direct children of dir at ref, or in the index.
func (e *Exec) ListTree(ctx context.Context, ref, dir string) ([]Entry, error) {
	if ref == "" {
		return e.listIndex(ctx, dir)
	}
	spec := ref + ":" + dir
	if dir == "" || dir == "." {
		spec = ref
	}
	out, err := e.run(ctx, "ls-tree", "-z", spec)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", spec, ErrNotFound)
	}
	var entries []Entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// "<mode> <type> <hash>\t<name>"
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(string(rec[:tab]))
		if len(meta) < 2 {
			continue
		}
		entries = append(entries, Entry{Name: string(rec[tab+1:]), IsDir: meta[1] == "tree"})
	}
	return entries, nil
}

func (e *Exec) listIndex(ctx context.Context, dir string) ([]Entry, error) {
	args := []string{"ls-files", "-z"}
	prefix := ""
	if dir != "" && dir != "." {
		prefix = strings.TrimSuffix(dir, "/") + "/"
		args = append(args, "--", prefix)
	}
	out, err := e.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var entries []Entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		p := string(rec)
		if p == "" || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, isDir := rest, false
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			name, isDir = rest[:i], true
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, Entry{Name: name, IsDir: isDir})
	}
	if len(entries) == 0 && prefix != "" {
		return nil, fmt.Errorf("%s: %w", dir, ErrNotFound)
	}
	return entries, nil
}

// StagedPaths lists added, copied, modified, renamed and deleted paths in the index.
func (e *Exec) StagedPaths(ctx context.Context) ([]string, error) {
	out, err := e.run(ctx, "diff", "--cached", "--name-only", "--diff-filter=ACMRD", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) > 0 {
			paths = append(paths, string(rec))
		}
	}
	return paths, nil
}

// ConfigSet sets a local config key.
func (e *Exec) ConfigSet(ctx context.Context, key, value string) error {
	_, err := e.run(ctx, "config", "--local", key, value)
	return err
}

// ResolveBase returns the commit to diff against: merge-base(base, HEAD) when
// useMergeBase is set and available, else the tip of base. The second return
// says whether the fallback happened.
func ResolveBase(ctx context.Context, g Git, base string, useMergeBase bool) (sha string, fellBack bool, err error) {
	tip, err := g.RevParse(ctx, base)
	if err != nil {
		return "", false, err
	}
	if !useMergeBase {
		return tip, false, nil
	}
	mb, err := g.MergeBase(ctx, base, "HEAD")
	if err != nil {
		return tip, true, nil
	}
	return mb, false, nil
}

// IsNotFound reports whether err is a missing ref or path.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
