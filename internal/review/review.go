// Package review compares the composed Kong config of each environment
// between a git base and the current side (worktree or a ref). Because both
// sides are composed, splitting a file or reordering entities shows no
// semantic change, while a real change appears once regardless of layout.
package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/m0un10/kongctl/internal/compose"
	"github.com/m0un10/kongctl/internal/diffx"
	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/layout"
)

// Status of one environment.
type Status int

const (
	StatusUnchanged Status = iota
	StatusChanged
	StatusAdded
	StatusDeleted
	StatusAbsent
	StatusError
)

// Result for one environment.
type Result struct {
	Env    string
	Status Status
	// Semantic is the composed, key-sorted diff ("" when none).
	Semantic string
	// Raw is the concatenated per-file unified diff ("" when none).
	Raw string
	// Identical is true when every source file is byte-identical.
	Identical bool
	Err       error
}

// SemanticChanged reports a real difference.
func (r Result) SemanticChanged() bool { return r.Semantic != "" }

// RawChanged reports any byte difference in the sources.
func (r Result) RawChanged() bool { return !r.Identical }

// Options for Compare.
type Options struct {
	// Base is the resolved base commit.
	Base string
	// Head is the current side; nil means use HeadFS.
	HeadFS fs.FS
}

// Compare diffs one environment.
func Compare(ctx context.Context, g gitx.Git, l layout.Layout, env string, opts Options) Result {
	baseFS := gitx.NewFS(ctx, g, opts.Base)
	headFS := opts.HeadFS
	res := Result{Env: env}

	baseFiles, baseErr := sourceFiles(baseFS, l, env)
	headFiles, headErr := sourceFiles(headFS, l, env)
	baseExists := baseErr == nil && len(baseFiles) > 0
	headExists := headErr == nil && len(headFiles) > 0
	switch {
	case !baseExists && !headExists:
		res.Status = StatusAbsent
		res.Identical = true
		return res
	case !baseExists:
		res.Status = StatusAdded
		return res
	case !headExists:
		res.Status = StatusDeleted
		return res
	}

	rel := l.SourceDirFor(env)
	baseRes, err := compose.Compose(baseFS, l, env, compose.Options{})
	if err != nil {
		res.Status, res.Err = StatusError, fmt.Errorf("base side does not compose: %w", err)
		return res
	}
	headRes, err := compose.Compose(headFS, l, env, compose.Options{})
	if err != nil {
		res.Status, res.Err = StatusError, fmt.Errorf("branch side does not compose: %w", err)
		return res
	}
	baseText, err := baseRes.Canonical()
	if err != nil {
		res.Status, res.Err = StatusError, err
		return res
	}
	headText, err := headRes.Canonical()
	if err != nil {
		res.Status, res.Err = StatusError, err
		return res
	}
	sem, _, err := diffx.Semantic(baseText, headText, "a/"+rel+" (composed)", "b/"+rel+" (composed)")
	if err != nil {
		res.Status, res.Err = StatusError, err
		return res
	}
	res.Semantic = sem

	// Raw: every file on either side, in name order.
	names := map[string]bool{}
	for _, f := range baseFiles {
		names[f] = true
	}
	for _, f := range headFiles {
		names[f] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	res.Identical = true
	for _, n := range sorted {
		a, aerr := fs.ReadFile(baseFS, n)
		b, berr := fs.ReadFile(headFS, n)
		switch {
		case aerr != nil && berr != nil:
			continue
		case aerr != nil:
			res.Identical = false
			res.Raw += diffx.Unified(nil, b, "/dev/null", "b/"+n)
		case berr != nil:
			res.Identical = false
			res.Raw += diffx.Unified(a, nil, "a/"+n, "/dev/null")
		default:
			if d := diffx.Unified(a, b, "a/"+n, "b/"+n); d != "" {
				res.Identical = false
				res.Raw += d
			}
		}
	}
	// Patches and common files count towards "raw" too, via the composed
	// output; the semantic diff already reflects them.
	if res.Semantic != "" {
		res.Status = StatusChanged
	} else {
		res.Status = StatusUnchanged
	}
	return res
}

func sourceFiles(fsys fs.FS, l layout.Layout, env string) ([]string, error) {
	files, err := l.Sources(fsys, env)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for i := range files {
		files[i] = path.Clean(files[i])
	}
	return files, nil
}
