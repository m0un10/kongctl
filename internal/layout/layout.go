// Package layout resolves where Kong config sources live inside a repository.
// Every path is a template in which "{env}" is replaced by the environment
// name. Paths are slash-separated and relative to the repository root so they
// work on any fs.FS (the worktree, a git ref, or a test filesystem).
package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Layout describes the repository conventions.
type Layout struct {
	// SourceDir holds the decK files for one environment, e.g.
	// "environments/{env}/config/kong".
	SourceDir string
	// CommonDir, when set, holds files merged before every environment's own.
	CommonDir string
	// PatchesDir, when set, holds decK patch files applied after the merge.
	PatchesDir string
	// Globs are the file patterns matched inside a directory, non-recursive.
	Globs []string
	// EnvNameRegex constrains environment names (a path-traversal guard).
	EnvNameRegex *regexp.Regexp
	// RecheckPaths are repository paths whose change means every environment
	// must be re-checked (the config file, the lint ruleset).
	RecheckPaths []string
}

// Expand replaces {env} in a template.
func Expand(template, env string) string {
	return strings.ReplaceAll(template, "{env}", env)
}

// ValidateEnv rejects names that do not match EnvNameRegex.
func (l Layout) ValidateEnv(env string) error {
	if env == "" {
		return errors.New("environment name is empty")
	}
	if l.EnvNameRegex != nil && !l.EnvNameRegex.MatchString(env) {
		return fmt.Errorf("%q is not a safe environment name (must match %s)", env, l.EnvNameRegex)
	}
	return nil
}

// SourceDirFor is the environment's source directory.
func (l Layout) SourceDirFor(env string) string { return Expand(l.SourceDir, env) }

// PatchesDirFor is the environment's patches directory, or "" when disabled.
func (l Layout) PatchesDirFor(env string) string {
	if l.PatchesDir == "" {
		return ""
	}
	return Expand(l.PatchesDir, env)
}

// ListFiles returns the files directly inside dir that match Globs, sorted by
// name. A missing directory yields fs.ErrNotExist.
func (l Layout) ListFiles(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if l.matches(e.Name()) {
			out = append(out, path.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l Layout) matches(name string) bool {
	if len(l.Globs) == 0 {
		return true
	}
	for _, g := range l.Globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// Sources lists an environment's own files.
func (l Layout) Sources(fsys fs.FS, env string) ([]string, error) {
	return l.ListFiles(fsys, l.SourceDirFor(env))
}

// CommonSources lists the shared files, or nothing when CommonDir is unset or
// absent.
func (l Layout) CommonSources(fsys fs.FS) ([]string, error) {
	if l.CommonDir == "" {
		return nil, nil
	}
	files, err := l.ListFiles(fsys, l.CommonDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return files, err
}

// PatchSources lists an environment's patch files, or nothing when the
// patches directory is unset or absent.
func (l Layout) PatchSources(fsys fs.FS, env string) ([]string, error) {
	dir := l.PatchesDirFor(env)
	if dir == "" {
		return nil, nil
	}
	files, err := l.ListFiles(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return files, err
}

// DiscoverEnvs finds every environment that has a source directory, by
// walking the fixed prefix of SourceDir and testing each candidate name.
func (l Layout) DiscoverEnvs(fsys fs.FS) ([]string, error) {
	idx := strings.Index(l.SourceDir, "{env}")
	if idx < 0 {
		return nil, fmt.Errorf("layout.sourceDir %q does not contain {env}", l.SourceDir)
	}
	prefix := path.Clean(l.SourceDir[:idx])
	if prefix == "." || prefix == "/" || strings.HasSuffix(l.SourceDir[:idx], "/") {
		prefix = strings.TrimSuffix(l.SourceDir[:idx], "/")
		if prefix == "" {
			prefix = "."
		}
	}
	entries, err := fs.ReadDir(fsys, prefix)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var envs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		env := e.Name()
		if l.ValidateEnv(env) != nil {
			continue
		}
		if st, err := fs.Stat(fsys, l.SourceDirFor(env)); err == nil && st.IsDir() {
			envs = append(envs, env)
		}
	}
	sort.Strings(envs)
	return envs, nil
}

// EnvFromPath reports which environment a repository path belongs to, by
// matching it against the SourceDir template (and PatchesDir when set).
func (l Layout) EnvFromPath(p string) (string, bool) {
	for _, tmpl := range []string{l.SourceDir, l.PatchesDir} {
		if tmpl == "" {
			continue
		}
		re, err := templateRegex(tmpl)
		if err != nil {
			continue
		}
		if m := re.FindStringSubmatch(p); m != nil {
			env := m[1]
			if l.ValidateEnv(env) == nil {
				return env, true
			}
		}
	}
	return "", false
}

// IsCommonPath reports whether p lies inside CommonDir.
func (l Layout) IsCommonPath(p string) bool {
	return l.CommonDir != "" && strings.HasPrefix(p, strings.TrimSuffix(l.CommonDir, "/")+"/")
}

// IsRecheckPath reports whether p is one of the paths that force a full check.
func (l Layout) IsRecheckPath(p string) bool {
	for _, r := range l.RecheckPaths {
		if p == r {
			return true
		}
	}
	return false
}

func templateRegex(tmpl string) (*regexp.Regexp, error) {
	parts := strings.Split(tmpl, "{env}")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	return regexp.Compile("^" + strings.Join(parts, `([^/]+)`) + "/")
}
