package cli

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/hook"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/placeholder"
)

func (a *App) checkCmd() *cobra.Command {
	var staged bool
	var f manifestFlags
	cmd := &cobra.Command{
		Use:   "check [env...]",
		Short: "Compose, validate and lint environments with fake secrets",
		Long: `check never needs real secret values: unset placeholders are filled with
fake ones so the rendered config can still be validated and linted. With no
arguments every discovered (or configured) environment is checked.

--staged reads the sources from the git index rather than the worktree and
checks only the environments whose files are staged, which is what the
pre-commit hook runs. A staged change to the config file, the lint ruleset or
the common directory re-checks every environment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var applyErr error
			if err := a.setup(cmd, func(c *config.Config) { applyErr = f.apply(cmd, c) }); err != nil {
				return err
			}
			if applyErr != nil {
				return applyErr
			}
			var fsys fs.FS = os.DirFS(a.cfg.Root)
			envs := args
			if staged {
				g := a.git()
				plan, err := hook.StagedPlan(cmd.Context(), g, a.cfg.ResolvedLayout())
				if err != nil {
					return kerr.Usage(err)
				}
				fsys = gitx.NewFS(cmd.Context(), g, "")
				if !plan.All && len(plan.Envs) == 0 {
					a.log.Debug("no staged Kong config changes")
					return nil
				}
				if !plan.All {
					envs = plan.Envs
				}
			}
			if len(envs) == 0 {
				var err error
				envs, err = a.envs(fsys)
				if err != nil {
					return err
				}
			}
			l := a.cfg.ResolvedLayout()
			checked, failed := 0, 0
			for _, env := range envs {
				if err := l.ValidateEnv(env); err != nil {
					return kerr.Usage(err)
				}
				if _, err := fs.Stat(fsys, l.SourceDirFor(env)); err != nil {
					a.log.Info("skip " + env + ": no source directory")
					continue
				}
				checked++
				var buf bytes.Buffer
				p := a.pipeline(fsys, placeholder.Lookup(a.Lookup))
				p.Log = a.log.Capture(&buf)
				err := p.CheckEnv(cmd.Context(), env)
				if err != nil {
					failed++
					fmt.Fprintf(a.Stderr, "FAIL:  %s\n", env)
					a.replay(&buf)
					fmt.Fprintf(a.Stderr, "       %s ERROR: %s\n", a.log.Prefix(), err.Error())
					continue
				}
				if !a.quiet {
					fmt.Fprintf(a.Stderr, "ok:    %s\n", env)
					a.replay(&buf)
				}
			}
			if checked == 0 {
				return kerr.Usagef("no environments checked")
			}
			if failed > 0 {
				fmt.Fprintf(a.Stderr, "==> FAILED (%d environment(s) checked)\n", checked)
				return kerr.Configf("%d environment(s) failed", failed)
			}
			fmt.Fprintf(a.Stderr, "==> OK (%d environment(s) checked)\n", checked)
			return nil
		},
	}
	cmd.Flags().BoolVar(&staged, "staged", false, "check the staged tree (pre-commit mode)")
	f.bind(cmd)
	_ = cmd.Flags().MarkHidden("output")
	_ = cmd.Flags().MarkHidden("env-file")
	_ = cmd.Flags().MarkHidden("fake-secrets")
	return cmd
}

func (a *App) replay(buf *bytes.Buffer) {
	for _, line := range bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		fmt.Fprintf(a.Stderr, "       %s\n", line)
	}
}
