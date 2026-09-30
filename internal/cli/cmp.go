package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/cmp"
	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/kerr"
)

func (a *App) cmpCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cmp",
		Short: "Argo CD Config Management Plugin entry points",
		Long: `Reads the plugin parameters Argo CD passes as ARGOCD_ENV_* variables,
composes the named environment and writes ONE manifest to stdout. Everything
else goes to stderr, which Argo CD shows as plugin logs.

Secret values (Secret and ConfigMap kinds) come from plain environment
variables on the sidecar, never from plugin parameters. FAKE_SECRETS is only
honoured as a bare container variable; a prefixed copy is ignored.`,
	}
	root.AddCommand(a.cmpPhaseCmd("init"), a.cmpPhaseCmd("generate"))
	return root
}

func (a *App) cmpPhaseCmd(phase string) *cobra.Command {
	short := "Sanity checks before generate"
	if phase == "generate" {
		short = "Emit the manifest for the Application's environment"
	}
	return &cobra.Command{
		Use:   phase,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Argo CD parses stdout as manifests; make sure nothing but the
			// artifact reaches it.
			realOut := a.Stdout
			a.Stdout = a.Stderr
			if realOut == os.Stdout {
				os.Stdout = os.Stderr
			}

			cwd, err := a.Getwd()
			if err != nil {
				return kerr.Usage(err)
			}
			// Resolve plugin parameters against defaults first so the prefix
			// and parameter names can themselves be configured by env.
			probe := config.Defaults()
			_ = probe.ApplyEnv(a.Lookup)
			opts, err := cmp.FromEnv(a.Lookup, probe, cwd)
			if err != nil {
				return err
			}
			a.root = opts.RepoRoot
			if opts.ConfigFile != "" {
				a.configPath = opts.ConfigFile
			}
			if err := a.setup(cmd, func(c *config.Config) {
				// Re-apply parameters on top of the loaded file.
				if _, err := cmp.FromEnv(a.Lookup, c, cwd); err == nil && opts.FakeSecrets {
					c.FakeSecrets = true
				}
			}); err != nil {
				return err
			}
			opts.LogWarnings(a.log, a.cfg)
			l := a.cfg.ResolvedLayout()
			if err := l.ValidateEnv(opts.Env); err != nil {
				return kerr.Usage(err)
			}
			fsys := os.DirFS(a.cfg.Root)
			a.log.Info(fmt.Sprintf("phase=%s env=%s root=%s kind=%s name=%s key=%s app=%s",
				phase, opts.Env, a.cfg.Root, a.cfg.Manifest.Kind, a.cfg.Manifest.Name, a.cfg.Manifest.Key, envOr(a.Lookup, "ARGOCD_APP_NAME", "<unset>")))
			if _, err := fs.Stat(fsys, l.SourceDirFor(opts.Env)); err != nil {
				if envs, derr := l.DiscoverEnvs(fsys); derr == nil && len(envs) > 0 {
					a.log.Info("environments present: " + strings.Join(envs, " "))
				}
				return kerr.Configf("no sources at %s for env %q", l.SourceDirFor(opts.Env), opts.Env)
			}
			if phase == "init" {
				a.log.Info(versionLine())
				a.log.Info("init complete")
				return nil
			}
			p := a.pipeline(fsys, nil)
			out, err := p.Manifest(cmd.Context(), opts.Env, a.cfg.FakeSecrets)
			if err != nil {
				return err
			}
			if _, err := realOut.Write(out); err != nil {
				return kerr.Usage(err)
			}
			a.log.Info("done")
			return nil
		},
	}
}

func envOr(lookup config.Lookup, name, def string) string {
	if v, ok := lookup(name); ok && v != "" {
		return v
	}
	return def
}

func versionLine() string {
	line := "kongctl " + Version
	if Commit != "" {
		line += " (" + Commit + ")"
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			switch d.Path {
			case "github.com/kong/go-apiops", "github.com/kong/go-database-reconciler", "github.com/daveshanley/vacuum":
				line += fmt.Sprintf(" %s@%s", filepath.Base(d.Path), d.Version)
			}
		}
	}
	return line
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(a.Stdout, versionLine())
			if Date != "" {
				fmt.Fprintln(a.Stdout, "built "+Date)
			}
			return nil
		},
	}
}
