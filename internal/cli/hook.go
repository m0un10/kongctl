package cli

import (
	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/hook"
	"github.com/m0un10/kongctl/internal/kerr"
)

func (a *App) hookCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hook",
		Short: "Git hook helpers",
	}
	var hooksDir string
	install := &cobra.Command{
		Use:   "install",
		Short: "Write .githooks/pre-commit (if absent) and set core.hooksPath",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, nil); err != nil {
				return err
			}
			written, err := hook.Install(cmd.Context(), a.git(), hooksDir)
			if err != nil {
				return kerr.Usage(err)
			}
			if written {
				a.log.Info("wrote " + hooksDir + "/pre-commit")
			} else {
				a.log.Info(hooksDir + "/pre-commit already exists, left as is")
			}
			a.log.Info("core.hooksPath = " + hooksDir)
			return nil
		},
	}
	install.Flags().StringVar(&hooksDir, "hooks-path", ".githooks", "directory for the versioned hooks")

	run := &cobra.Command{
		Use:   "run pre-commit",
		Short: "Run a hook directly (pre-commit = check --staged)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "pre-commit" {
				return kerr.Usagef("unknown hook %q", args[0])
			}
			check := a.checkCmd()
			check.SetContext(cmd.Context())
			_ = check.Flags().Set("staged", "true")
			return check.RunE(check, nil)
		},
	}
	root.AddCommand(install, run)
	return root
}
