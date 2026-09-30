// Package hook implements the pre-commit behaviour: work out which
// environments the staged changes touch, and install the hook script.
package hook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/layout"
)

// Plan says what a staged check must cover.
type Plan struct {
	// Envs to check; empty with All=false means nothing relevant is staged.
	Envs []string
	// All means every environment must be re-checked.
	All bool
}

// StagedPlan derives the plan from the index.
func StagedPlan(ctx context.Context, g gitx.Git, l layout.Layout) (Plan, error) {
	paths, err := g.StagedPaths(ctx)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	seen := map[string]bool{}
	for _, p := range paths {
		if l.IsRecheckPath(p) || l.IsCommonPath(p) {
			plan.All = true
			continue
		}
		if env, ok := l.EnvFromPath(p); ok && !seen[env] {
			seen[env] = true
			plan.Envs = append(plan.Envs, env)
		}
	}
	sort.Strings(plan.Envs)
	return plan, nil
}

// Script is the pre-commit hook content.
const Script = `#!/bin/sh
# Installed by kongctl hook install. Checks the staged Kong config sources.
exec kongctl check --staged -q
`

// Install writes the hook (unless present) and points core.hooksPath at it.
func Install(ctx context.Context, g gitx.Git, hooksDir string) (written bool, err error) {
	top, err := g.TopLevel(ctx)
	if err != nil {
		return false, err
	}
	dir := filepath.Join(top, filepath.FromSlash(hooksDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	target := filepath.Join(dir, "pre-commit")
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(target, []byte(Script), 0o755); err != nil {
			return false, err
		}
		written = true
	} else if err != nil {
		return false, err
	} else if err := os.Chmod(target, 0o755); err != nil {
		return false, err
	}
	if err := g.ConfigSet(ctx, "core.hooksPath", hooksDir); err != nil {
		return written, fmt.Errorf("setting core.hooksPath: %w", err)
	}
	return written, nil
}
