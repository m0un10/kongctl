// Package pipeline wires the stages together: compose, then either render
// (substitute real or fake values) or template (ExternalSecret mode), then
// validate and lint, then wrap in a manifest. Every command and the CMP go
// through here, so local runs and cluster syncs share one code path.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/m0un10/kongctl/internal/compose"
	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/lint"
	"github.com/m0un10/kongctl/internal/logx"
	"github.com/m0un10/kongctl/internal/manifest"
	"github.com/m0un10/kongctl/internal/placeholder"
	"github.com/m0un10/kongctl/internal/validate"
	"github.com/m0un10/kongctl/internal/yamlx"
)

// Pipeline holds everything a run needs.
type Pipeline struct {
	FS     fs.FS
	Config *config.Config
	Log    *logx.Logger
	// Lookup resolves placeholder values (process env, env-file).
	Lookup placeholder.Lookup
}

// Syntax builds the placeholder syntax from config.
func (p *Pipeline) Syntax() placeholder.Syntax {
	return placeholder.Syntax{
		Mode:   placeholder.Mode(p.Config.Placeholders.Mode),
		Open:   p.Config.Placeholders.Open,
		Close:  p.Config.Placeholders.Close,
		NameRE: regexp.MustCompile(p.Config.Placeholders.NameRegex),
	}
}

// Compose merges an environment's sources.
func (p *Pipeline) Compose(env string) (*compose.Result, error) {
	l := p.Config.ResolvedLayout()
	if err := l.ValidateEnv(env); err != nil {
		return nil, kerr.Usage(err)
	}
	res, err := compose.Compose(p.FS, l, env, compose.Options{})
	if err != nil {
		if errors.Is(err, compose.ErrNoSources) {
			return nil, kerr.Config(err)
		}
		return nil, kerr.Config(err)
	}
	n := len(res.Files)
	p.Log.Info(fmt.Sprintf("compose %s: %d file(s) from %s", env, n, l.SourceDirFor(env)))
	if len(res.Patches) > 0 {
		p.Log.Info(fmt.Sprintf("patch %s: %d file(s) from %s", env, len(res.Patches), l.PatchesDirFor(env)))
	}
	return res, nil
}

// Placeholders lists the names an environment needs.
func (p *Pipeline) Placeholders(env string) ([]string, error) {
	res, err := p.Compose(env)
	if err != nil {
		return nil, err
	}
	rep := placeholder.Scan(res.Data, p.Syntax())
	if err := placeholder.OffenderError(rep.Offenders); err != nil {
		return nil, kerr.Config(err)
	}
	return rep.Names, nil
}

// Render substitutes placeholders. fake fills missing values.
func (p *Pipeline) Render(env string, data map[string]any, fake bool) ([]byte, error) {
	out, rep, err := placeholder.Render(data, p.Syntax(), p.Lookup, placeholder.RenderOptions{
		Fake:       fake,
		FakePrefix: p.Config.Placeholders.FakePrefix,
	})
	if len(rep.Faked) > 0 {
		p.Log.Warn("fake values substituted for: " + strings.Join(rep.Faked, " "))
	}
	if err != nil {
		if len(rep.Missing) > 0 {
			return nil, kerr.Configf("unset or empty placeholders for %q: %s", env, strings.Join(rep.Missing, " "))
		}
		return nil, kerr.Config(err)
	}
	p.Log.Info(fmt.Sprintf("render %s: substituted %d placeholder(s)", env, len(rep.Substituted)))
	m, ok := out.(map[string]any)
	if !ok {
		return nil, kerr.Configf("rendered config is not a mapping")
	}
	return yamlx.Canonical(m)
}

// Check runs validate and lint on a rendered config.
func (p *Pipeline) Check(ctx context.Context, env string, rendered []byte) error {
	if p.Config.Validate.Skip {
		p.Log.Info("validate " + env + ": skipped")
	} else {
		if err := validate.Validate(ctx, rendered, validate.Options{KongVersion: p.Config.Validate.KongVersion}); err != nil {
			var ve *validate.Errors
			if errors.As(err, &ve) {
				for _, m := range ve.Messages {
					p.Log.Raw("    " + m)
				}
				return kerr.Configf("deck file validate failed for %q", env)
			}
			return kerr.Usage(err)
		}
		p.Log.Info("validate " + env + ": ok")
	}

	if p.Config.Lint.Skip {
		p.Log.Info("lint " + env + ": skipped")
		return nil
	}
	ruleset, err := fs.ReadFile(p.FS, strings.TrimPrefix(p.Config.Lint.Ruleset, "./"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			p.Log.Info(fmt.Sprintf("lint %s: skipped (no ruleset at %s)", env, p.Config.Lint.Ruleset))
			return nil
		}
		return kerr.Usage(err)
	}
	rep, err := lint.Lint(rendered, ruleset, p.Config.Lint.FailSeverity)
	if err != nil {
		return kerr.Usage(fmt.Errorf("lint: %w", err))
	}
	if rep.Skipped {
		p.Log.Warn("lint " + env + ": state file is empty, skipping")
		return nil
	}
	p.Log.Raw(rep.String())
	if rep.Failed() {
		if p.Config.Lint.AllowFailure {
			p.Log.Warn(fmt.Sprintf("lint findings for %q (continuing: lint.allowFailure)", env))
			return nil
		}
		return kerr.Configf("lint findings for %q (set lint.allowFailure to sync anyway)", env)
	}
	p.Log.Info("lint " + env + ": ok")
	return nil
}

// Manifest produces the delivery object for an environment.
//
// Secret and ConfigMap: the config is rendered with real values (or fake ones
// when fake is set), checked, and embedded.
//
// ExternalSecret: the config is checked on a fake-rendered copy, then the
// composed config has its placeholders turned into template expressions and
// is embedded with one data entry per placeholder. No real values are needed.
func (p *Pipeline) Manifest(ctx context.Context, env string, fake bool) ([]byte, error) {
	res, err := p.Compose(env)
	if err != nil {
		return nil, err
	}
	mc := p.Config.Manifest
	spec := manifest.Spec{
		Kind:        mc.Kind,
		Name:        mc.Name,
		Key:         mc.Key,
		Labels:      mc.Labels,
		Annotations: mc.Annotations,
		Env:         env,
	}
	switch mc.Kind {
	case manifest.KindSecret, manifest.KindConfigMap:
		rendered, err := p.Render(env, res.Data, fake)
		if err != nil {
			return nil, err
		}
		if err := p.Check(ctx, env, rendered); err != nil {
			return nil, err
		}
		spec.Data = rendered
		out, err := manifest.Build(spec)
		if err != nil {
			return nil, kerr.Usage(err)
		}
		p.Log.Info(fmt.Sprintf("manifest %s: %s/%s key=%s", env, mc.Kind, mc.Name, mc.Key))
		return out, nil
	case manifest.KindExternalSecret:
		// Validation needs a parseable file, so check a fake-rendered copy.
		rendered, err := p.Render(env, res.Data, true)
		if err != nil {
			return nil, err
		}
		if err := p.Check(ctx, env, rendered); err != nil {
			return nil, err
		}
		templated, names, err := p.Template(env, res.Data)
		if err != nil {
			return nil, err
		}
		spec.Data = templated
		es := p.externalSecretSpec(names)
		out, err := manifest.BuildExternalSecret(spec, es)
		if err != nil {
			return nil, kerr.Usage(err)
		}
		p.Log.Info(fmt.Sprintf("manifest %s: ExternalSecret/%s key=%s, %d data entrie(s)", env, mc.Name, mc.Key, len(names)))
		return out, nil
	}
	return nil, kerr.Usagef("unsupported manifest kind %q", mc.Kind)
}

const (
	sentinelPrefix = "__KONGCTL_PH_"
	sentinelBrace  = "__KONGCTL_LBRACE__"
)

// Template turns placeholders into ExternalSecret template expressions. It
// serialises with sentinel tokens standing in for the expressions so the YAML
// encoder never quotes them, then swaps the sentinels for the expressions.
func (p *Pipeline) Template(env string, data map[string]any) ([]byte, []string, error) {
	es := p.Config.ExternalSec
	type slot struct {
		name  string
		whole bool
	}
	var slots []slot
	embedded := 0
	out, rep, err := placeholder.Expressions(data, p.Syntax(), func(name string, whole bool) string {
		slots = append(slots, slot{name, whole})
		if !whole {
			embedded++
		}
		return fmt.Sprintf("%s%d__", sentinelPrefix, len(slots)-1)
	}, func(s string) string {
		return strings.ReplaceAll(s, "{{", sentinelBrace)
	})
	if err != nil {
		return nil, nil, kerr.Config(err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		return nil, nil, kerr.Configf("composed config is not a mapping")
	}
	text, err := yamlx.Canonical(m)
	if err != nil {
		return nil, nil, kerr.Usage(err)
	}
	s := string(text)
	for i, sl := range slots {
		expr := es.Expression
		if !sl.whole {
			expr = es.EmbeddedExpression
		}
		s = strings.ReplaceAll(s, fmt.Sprintf("%s%d__", sentinelPrefix, i), strings.ReplaceAll(expr, "{name}", sl.name))
	}
	s = strings.ReplaceAll(s, sentinelBrace, `{{ "{{" }}`)
	if strings.Contains(s, sentinelPrefix) {
		return nil, nil, kerr.Usagef("template sentinel leaked into the output for %q", env)
	}
	if embedded > 0 {
		p.Log.Warn(fmt.Sprintf("%d embedded placeholder(s) in %q are rendered without quoting; the store values must be safe inside their YAML string", embedded, env))
	}
	p.Log.Info(fmt.Sprintf("template %s: %d placeholder(s) as template expressions", env, len(slots)))
	return []byte(s), rep.Names, nil
}

func (p *Pipeline) externalSecretSpec(names []string) manifest.ExternalSecretSpec {
	es := p.Config.ExternalSec
	entries := make(map[string]manifest.RemoteRef, len(names))
	for _, n := range names {
		ref := manifest.RemoteRef{
			Key:                strings.ReplaceAll(es.RemoteKey, "{name}", n),
			ConversionStrategy: es.ConversionStrategy,
			DecodingStrategy:   es.DecodingStrategy,
			MetadataPolicy:     es.MetadataPolicy,
		}
		if o, ok := es.RemoteKeyOverrides[n]; ok {
			if o.Key != "" {
				ref.Key = o.Key
			}
			ref.Property = o.Property
			ref.Version = o.Version
		}
		entries[n] = ref
	}
	return manifest.ExternalSecretSpec{
		APIVersion:      es.APIVersion,
		RefreshInterval: es.RefreshInterval,
		StoreKind:       es.StoreKind,
		StoreName:       es.StoreName,
		TargetName:      es.TargetName,
		CreationPolicy:  es.CreationPolicy,
		EngineVersion:   es.EngineVersion,
		Entries:         entries,
	}
}

// CheckEnv composes, renders with fake values, validates and lints one
// environment, and in ExternalSecret mode also builds the manifest so a bad
// store configuration fails here rather than at sync time.
func (p *Pipeline) CheckEnv(ctx context.Context, env string) error {
	res, err := p.Compose(env)
	if err != nil {
		return err
	}
	rendered, err := p.Render(env, res.Data, true)
	if err != nil {
		return err
	}
	if err := p.Check(ctx, env, rendered); err != nil {
		return err
	}
	if p.Config.Manifest.Kind == manifest.KindExternalSecret {
		templated, names, err := p.Template(env, res.Data)
		if err != nil {
			return err
		}
		spec := manifest.Spec{Kind: manifest.KindExternalSecret, Name: p.Config.Manifest.Name, Key: p.Config.Manifest.Key, Env: env, Data: templated}
		if _, err := manifest.BuildExternalSecret(spec, p.externalSecretSpec(names)); err != nil {
			return kerr.Usage(err)
		}
	}
	return nil
}
