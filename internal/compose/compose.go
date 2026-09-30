// Package compose merges decK files the way `deck file merge` does, then
// applies decK patch files, reading everything from an fs.FS so the same code
// serves the worktree, the git index and a git ref.
//
// Merge is a port of github.com/kong/go-apiops/merge (Apache-2.0, Kong Inc.),
// minus two things: reading from disk, and the substitution of
// ${{ env "DECK_*" }} inside _format_version, which kongctl leaves alone.
package compose

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/kong/go-apiops/deckformat"

	"github.com/m0un10/kongctl/internal/layout"
	"github.com/m0un10/kongctl/internal/patch"
	"github.com/m0un10/kongctl/internal/yamlx"
)

// Source is one input file.
type Source struct {
	Name string
	Data []byte
}

// Result is a composed configuration.
type Result struct {
	// Data is the merged, patched configuration as a JSON-shaped map.
	Data map[string]any
	// Files are the merged inputs in order; Patches the patch files applied.
	Files   []string
	Patches []string
}

// Canonical returns the result in decK's serialised form.
func (r *Result) Canonical() ([]byte, error) { return yamlx.Canonical(r.Data) }

// Merge concatenates top-level arrays across files in order; other keys are
// overwritten by later files. _format_version majors must agree and the
// result carries the highest minor. There is no de-duplication: an entity
// present in two files ends up twice, and validate reports it.
func Merge(sources []Source) (map[string]any, error) {
	if len(sources) == 0 {
		return nil, errors.New("no files to merge")
	}
	var result map[string]any
	minor := 0
	for _, src := range sources {
		data, err := yamlx.Deserialize(src.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Name, err)
		}
		if result == nil {
			result = make(map[string]any)
			if v := data[deckformat.TransformKey]; v != nil {
				result[deckformat.TransformKey] = v
			}
			if v := data[deckformat.VersionKey]; v != nil {
				result[deckformat.VersionKey] = v
			}
		}
		if err := deckformat.CompatibleFile(result, data); err != nil {
			return nil, fmt.Errorf("failed to merge %s: %w", src.Name, err)
		}
		if _, m, _ := deckformat.ParseFormatVersion(data); m > minor {
			minor = m
		}
		result = merge2(result, data)
	}
	if result[deckformat.VersionKey] != nil {
		major, _, _ := deckformat.ParseFormatVersion(result)
		if major == 0 {
			delete(result, deckformat.VersionKey)
		} else {
			result[deckformat.VersionKey] = fmt.Sprint(major, ".", minor)
		}
	}
	return result, nil
}

func merge2(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if existing, ok := out[k]; ok {
			if ea, ok := existing.([]any); ok {
				if eb, ok := v.([]any); ok {
					out[k] = append(append([]any{}, ea...), eb...)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

// Options tunes Compose.
type Options struct {
	NoCommon  bool
	NoPatches bool
}

// ErrNoSources is returned when an environment has no matching files.
var ErrNoSources = errors.New("no source files")

// Compose reads the common files (if configured), the environment's files and
// its patches from fsys, merges and patches them.
func Compose(fsys fs.FS, l layout.Layout, env string, opts Options) (*Result, error) {
	if err := l.ValidateEnv(env); err != nil {
		return nil, err
	}
	dir := l.SourceDirFor(env)
	envFiles, err := l.Sources(fsys, env)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no source directory for %q: %s: %w", env, dir, ErrNoSources)
		}
		return nil, err
	}
	if len(envFiles) == 0 {
		return nil, fmt.Errorf("no matching files in %s: %w", dir, ErrNoSources)
	}
	var files []string
	if !opts.NoCommon {
		common, err := l.CommonSources(fsys)
		if err != nil {
			return nil, err
		}
		files = append(files, common...)
	}
	files = append(files, envFiles...)

	sources := make([]Source, 0, len(files))
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		sources = append(sources, Source{Name: f, Data: data})
	}
	merged, err := Merge(sources)
	if err != nil {
		return nil, err
	}
	res := &Result{Data: merged, Files: files}

	if !opts.NoPatches {
		patches, err := l.PatchSources(fsys, env)
		if err != nil {
			return nil, err
		}
		if len(patches) > 0 {
			psrc := make([]patch.Source, 0, len(patches))
			for _, p := range patches {
				data, err := fs.ReadFile(fsys, p)
				if err != nil {
					return nil, err
				}
				psrc = append(psrc, patch.Source{Name: p, Data: data})
			}
			res.Data, err = patch.Apply(res.Data, psrc)
			if err != nil {
				return nil, err
			}
			res.Patches = patches
		}
	}
	return res, nil
}
