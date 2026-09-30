// Package validate performs the same offline check as `deck file validate`:
// JSON-schema validation of the state file, then building a Kong state from
// it so that broken references and duplicate entities surface. No Kong
// connection is needed.
package validate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/kong/go-database-reconciler/pkg/dump"
	"github.com/kong/go-database-reconciler/pkg/file"
	"github.com/kong/go-database-reconciler/pkg/state"
	"github.com/kong/go-database-reconciler/pkg/utils"
)

// Options tunes validation.
type Options struct {
	// KongVersion enables version-specific checks; empty means "unspecified",
	// which matches decK's default.
	KongVersion string
}

// Errors is the list of problems found.
type Errors struct {
	Messages []string
}

func (e *Errors) Error() string {
	return "validation failed:\n    " + strings.Join(e.Messages, "\n    ")
}

// Validate checks a rendered declarative config.
func Validate(ctx context.Context, rendered []byte, opts Options) error {
	var version semver.Version
	if opts.KongVersion != "" {
		v, err := semver.ParseTolerant(opts.KongVersion)
		if err != nil {
			return fmt.Errorf("validate.kongVersion %q: %w", opts.KongVersion, err)
		}
		version = v
	}
	content, err := file.GetContentFromReader(bytes.NewReader(rendered), file.EnvVarsSkip)
	if err != nil {
		return &Errors{Messages: flatten(err)}
	}
	empty, err := state.NewKongState()
	if err != nil {
		return err
	}
	raw, err := file.Get(ctx, content, file.RenderConfig{
		CurrentState: empty,
		KongVersion:  version,
	}, dump.Config{}, nil)
	if err != nil {
		return &Errors{Messages: flatten(err)}
	}
	if _, err := state.Get(raw); err != nil {
		return &Errors{Messages: flatten(err)}
	}
	return nil
}

func flatten(err error) []string {
	var arr utils.ErrArray
	if errors.As(err, &arr) {
		var out []string
		for _, e := range arr.Errors {
			out = append(out, flatten(e)...)
		}
		return out
	}
	var parr *utils.ErrArray
	if errors.As(err, &parr) {
		var out []string
		for _, e := range parr.Errors {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return []string{err.Error()}
}
