package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/manifest"
)

func (a *App) composeCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "compose <env>",
		Short: "Merge an environment's decK files (placeholders intact)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, nil); err != nil {
				return err
			}
			env, err := singleEnv(args)
			if err != nil {
				return err
			}
			res, err := a.pipeline(os.DirFS(a.cfg.Root), nil).Compose(env)
			if err != nil {
				return err
			}
			out, err := res.Canonical()
			if err != nil {
				return kerr.Usage(err)
			}
			return a.emit(out, output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	return cmd
}

func (a *App) placeholdersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "placeholders <env>",
		Short: "List the ${NAME} placeholders an environment needs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, nil); err != nil {
				return err
			}
			env, err := singleEnv(args)
			if err != nil {
				return err
			}
			names, err := a.pipeline(os.DirFS(a.cfg.Root), nil).Placeholders(env)
			if err != nil {
				return err
			}
			for _, n := range names {
				fmt.Fprintln(a.Stdout, n)
			}
			return nil
		},
	}
}

func (a *App) renderCmd() *cobra.Command {
	var output, envFile string
	var fake bool
	cmd := &cobra.Command{
		Use:   "render <env>",
		Short: "Compose and substitute placeholders from the environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, func(c *config.Config) {
				if fake {
					c.FakeSecrets = true
				}
			}); err != nil {
				return err
			}
			env, err := singleEnv(args)
			if err != nil {
				return err
			}
			lookup, err := a.loadEnvFile(envFile)
			if err != nil {
				return err
			}
			p := a.pipeline(os.DirFS(a.cfg.Root), lookup)
			res, err := p.Compose(env)
			if err != nil {
				return err
			}
			out, err := p.Render(env, res.Data, a.cfg.FakeSecrets)
			if err != nil {
				return err
			}
			return a.emit(out, output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	cmd.Flags().StringVar(&envFile, "env-file", "", "NAME=value file consulted before the process environment")
	cmd.Flags().BoolVar(&fake, "fake-secrets", false, "fill unset placeholders with fake values")
	return cmd
}

// manifestFlags are shared by manifest and cmp generate.
type manifestFlags struct {
	output, envFile, kind, name, key, ruleset, failSeverity string
	labels, annotations                                     []string
	fake, skipValidate, skipLint, allowLintFailure          bool
}

func (f *manifestFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVarP(&f.output, "output", "o", "", "write to a file instead of stdout")
	fl.StringVar(&f.envFile, "env-file", "", "NAME=value file consulted before the process environment")
	fl.BoolVar(&f.fake, "fake-secrets", false, "fill unset placeholders with fake values (Secret and ConfigMap kinds)")
	fl.StringVar(&f.kind, "kind", "", "Secret, ConfigMap or ExternalSecret")
	fl.StringVarP(&f.name, "name", "n", "", "object name")
	fl.StringVarP(&f.key, "key", "k", "", "data key holding the config")
	fl.StringArrayVar(&f.labels, "label", nil, "label k=v (repeatable; {env} expands)")
	fl.StringArrayVar(&f.annotations, "annotation", nil, "annotation k=v (repeatable; {env} expands)")
	fl.BoolVar(&f.skipValidate, "skip-validate", false, "skip schema validation")
	fl.BoolVar(&f.skipLint, "skip-lint", false, "skip the lint ruleset")
	fl.BoolVar(&f.allowLintFailure, "allow-lint-failure", false, "warn instead of failing on lint findings")
	fl.StringVar(&f.ruleset, "ruleset", "", "lint ruleset path")
	fl.StringVar(&f.failSeverity, "fail-severity", "", "lint severity that fails: hint, info, warn, error")
}

func (f *manifestFlags) apply(cmd *cobra.Command, c *config.Config) error {
	changed := cmd.Flags().Changed
	if f.fake {
		c.FakeSecrets = true
	}
	if changed("kind") {
		c.Manifest.Kind = f.kind
	}
	if changed("name") {
		c.Manifest.Name = f.name
	}
	if changed("key") {
		c.Manifest.Key = f.key
	}
	if changed("skip-validate") {
		c.Validate.Skip = f.skipValidate
	}
	if changed("skip-lint") {
		c.Lint.Skip = f.skipLint
	}
	if changed("allow-lint-failure") {
		c.Lint.AllowFailure = f.allowLintFailure
	}
	if changed("ruleset") {
		c.Lint.Ruleset = f.ruleset
	}
	if changed("fail-severity") {
		c.Lint.FailSeverity = f.failSeverity
	}
	for _, kv := range f.labels {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return kerr.Usagef("--label %q is not k=v", kv)
		}
		if c.Manifest.Labels == nil {
			c.Manifest.Labels = map[string]string{}
		}
		c.Manifest.Labels[k] = v
	}
	for _, kv := range f.annotations {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return kerr.Usagef("--annotation %q is not k=v", kv)
		}
		if c.Manifest.Annotations == nil {
			c.Manifest.Annotations = map[string]string{}
		}
		c.Manifest.Annotations[k] = v
	}
	return nil
}

func (a *App) manifestCmd() *cobra.Command {
	var f manifestFlags
	cmd := &cobra.Command{
		Use:   "manifest <env>",
		Short: "Build the Kubernetes object that delivers the config",
		Long: `Secret and ConfigMap kinds render the config with real values from the
environment (or fake ones with --fake-secrets), validate and lint it, and
embed it. ExternalSecret validates and lints a fake-rendered copy, then
embeds the composed config as an External Secrets template with one data
entry per placeholder; no secret values are needed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var applyErr error
			if err := a.setup(cmd, func(c *config.Config) { applyErr = f.apply(cmd, c) }); err != nil {
				return err
			}
			if applyErr != nil {
				return applyErr
			}
			env, err := singleEnv(args)
			if err != nil {
				return err
			}
			lookup, err := a.loadEnvFile(f.envFile)
			if err != nil {
				return err
			}
			p := a.pipeline(os.DirFS(a.cfg.Root), lookup)
			out, err := p.Manifest(cmd.Context(), env, a.cfg.FakeSecrets)
			if err != nil {
				return err
			}
			return a.emit(out, f.output)
		},
	}
	f.bind(cmd)
	return cmd
}

// manifestKindHelp is used by cmp and docs.
func manifestKindHelp() string {
	return strings.Join([]string{manifest.KindSecret, manifest.KindConfigMap, manifest.KindExternalSecret}, ", ")
}

var _ = context.Background
