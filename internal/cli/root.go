// Package cli defines the cobra command tree. Commands parse flags, load
// configuration and call into the internal packages; they hold no business
// logic of their own.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/logx"
	"github.com/m0un10/kongctl/internal/pipeline"
	"github.com/m0un10/kongctl/internal/placeholder"
)

// Version information, set by the linker.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// App carries the injected I/O so the CLI is testable.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Lookup config.Lookup
	// Getwd returns the working directory; defaults to os.Getwd.
	Getwd func() (string, error)

	cfg        *config.Config
	log        *logx.Logger
	root       string
	configPath string
	quiet      bool
	logLevel   string
	envsFlag   string
}

// NewApp wires defaults.
func NewApp() *App {
	return &App{Stdout: os.Stdout, Stderr: os.Stderr, Lookup: os.LookupEnv, Getwd: os.Getwd}
}

// Command builds the root command.
func (a *App) Command() *cobra.Command {
	root := &cobra.Command{
		Use:   "kongctl",
		Short: "Compose, check and deliver Kong declarative config from a GitOps repo",
		Long: `kongctl composes Kong DB-less declarative config from plain decK files,
substitutes or templates ${NAME} placeholders, validates and lints the result
and wraps it in the Kubernetes object that delivers it: a Secret, a ConfigMap
or an External Secrets ExternalSecret. The same binary runs locally, in git
hooks, in CI and as an Argo CD Config Management Plugin.

stdout carries only the artifact a command produces; all logging goes to
stderr. Exit codes: 0 ok, 1 configuration problem, 2 usage or tooling.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&a.root, "root", "", "repository root (default: git toplevel, else the working directory)")
	pf.StringVar(&a.configPath, "config", "", "config file (default: <root>/"+config.DefaultFileName+" when present)")
	pf.BoolVarP(&a.quiet, "quiet", "q", false, "only log warnings and errors")
	pf.StringVar(&a.logLevel, "log-level", "", "debug, info, warn or error")
	pf.StringVarP(&a.envsFlag, "envs", "e", "", "comma-separated environment list (default: discover)")

	root.AddCommand(
		a.composeCmd(),
		a.placeholdersCmd(),
		a.renderCmd(),
		a.manifestCmd(),
		a.checkCmd(),
		a.diffCmd(),
		a.notateCmd(),
		a.normaliseCmd(),
		a.cmpCmd(),
		a.hookCmd(),
		a.versionCmd(),
	)
	return root
}

// Execute runs the CLI and returns the exit code.
func Execute(ctx context.Context, args []string) int {
	a := NewApp()
	cmd := a.Command()
	cmd.SetArgs(args)
	cmd.SetOut(a.Stderr)
	cmd.SetErr(a.Stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		prefix := "[kongctl] ERROR: "
		fmt.Fprintln(a.Stderr, prefix+err.Error())
		return kerr.ExitCode(err)
	}
	return 0
}

// setup loads configuration for a command. overrides run after env, before
// validation, so per-command flags win.
func (a *App) setup(cmd *cobra.Command, overrides func(*config.Config)) error {
	cfg := config.Defaults()
	root, err := a.resolveRoot(cmd.Context())
	if err != nil {
		return err
	}
	fsys := os.DirFS(root)
	cfgName := a.configPath
	explicit := cfgName != ""
	if !explicit {
		cfgName = config.DefaultFileName
	}
	if explicit && !filepath.IsAbs(cfgName) {
		cfgName = filepath.Join(root, cfgName)
	}
	var loadErr error
	if explicit {
		data, err := os.ReadFile(cfgName)
		if err != nil {
			return kerr.Usagef("config file: %w", err)
		}
		loadErr = cfg.LoadBytes(data, cfgName)
	} else {
		loadErr = cfg.LoadFile(fsys, cfgName)
		if errors.Is(loadErr, fs.ErrNotExist) {
			loadErr = nil
		}
	}
	if loadErr != nil {
		return kerr.Usage(loadErr)
	}
	if err := cfg.ApplyEnv(a.Lookup); err != nil {
		return kerr.Usage(err)
	}
	cfg.Root = root
	if a.envsFlag != "" {
		cfg.Envs = config.SplitList(a.envsFlag)
	}
	if a.logLevel != "" {
		cfg.Log.Level = a.logLevel
	}
	if overrides != nil {
		overrides(cfg)
	}
	if _, err := cfg.LoadStoreFile(fsys); err != nil {
		return kerr.Usage(err)
	}
	if err := cfg.Check(); err != nil {
		return kerr.Usage(err)
	}
	level, err := logx.ParseLevel(cfg.Log.Level)
	if err != nil {
		return kerr.Usage(err)
	}
	if a.quiet && level < logx.LevelWarn {
		level = logx.LevelWarn
	}
	a.cfg = cfg
	a.log = logx.New(a.Stderr, level, "[kongctl]")
	return nil
}

func (a *App) resolveRoot(ctx context.Context) (string, error) {
	if a.root != "" {
		abs, err := filepath.Abs(a.root)
		if err != nil {
			return "", kerr.Usage(err)
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return "", kerr.Usagef("root %q is not a directory", a.root)
		}
		return abs, nil
	}
	if v, ok := a.Lookup("KONGCTL_ROOT"); ok && v != "" {
		return filepath.Abs(v)
	}
	if v, ok := a.Lookup("KONG_CONFIG_ROOT"); ok && v != "" {
		return filepath.Abs(v)
	}
	cwd, err := a.Getwd()
	if err != nil {
		return "", kerr.Usage(err)
	}
	g := &gitx.Exec{Dir: cwd}
	if top, err := g.TopLevel(ctx); err == nil && top != "" {
		return top, nil
	}
	return cwd, nil
}

func (a *App) pipeline(fsys fs.FS, lookup placeholder.Lookup) *pipeline.Pipeline {
	if lookup == nil {
		lookup = placeholder.Lookup(a.Lookup)
	}
	return &pipeline.Pipeline{FS: fsys, Config: a.cfg, Log: a.log, Lookup: lookup}
}

func (a *App) git() gitx.Git { return &gitx.Exec{Dir: a.cfg.Root} }

// envs returns the configured or discovered environment list.
func (a *App) envs(fsys fs.FS) ([]string, error) {
	if len(a.cfg.Envs) > 0 {
		return a.cfg.Envs, nil
	}
	envs, err := a.cfg.ResolvedLayout().DiscoverEnvs(fsys)
	if err != nil {
		return nil, kerr.Usage(err)
	}
	return envs, nil
}

// emit writes the artifact to the output path or stdout.
func (a *App) emit(data []byte, output string) error {
	if output == "" {
		_, err := a.Stdout.Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return kerr.Usage(err)
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return kerr.Usage(err)
	}
	a.log.Info("wrote " + output)
	return nil
}

func singleEnv(args []string) (string, error) {
	if len(args) != 1 {
		return "", kerr.Usagef("expected exactly one environment, got %d", len(args))
	}
	return strings.TrimSpace(args[0]), nil
}

// loadEnvFile builds a lookup that prefers the env file over the process env.
func (a *App) loadEnvFile(path string) (placeholder.Lookup, error) {
	base := placeholder.Lookup(a.Lookup)
	if path == "" {
		return base, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, kerr.Usagef("env file: %w", err)
	}
	defer f.Close()
	values, err := placeholder.ParseEnvFile(f)
	if err != nil {
		return nil, kerr.Usagef("env file %s: %w", path, err)
	}
	a.log.Info("sourcing " + path)
	return placeholder.ChainLookup(placeholder.MapLookup(values), base), nil
}
