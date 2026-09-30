package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/diffx"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/yamlx"
)

func (a *App) normaliseCmd() *cobra.Command {
	var check, showDiff, backup bool
	cmd := &cobra.Command{
		Use:     "normalise [file...]",
		Aliases: []string{"normalize", "fmt"},
		Short:   "Rewrite source files in decK's canonical form",
		Long: `Each file is deserialised and re-serialised the way deck file merge
writes it: sorted keys, two-space indent, no defaults added, placeholders
untouched. With no arguments every environment's source files are
processed. --check exits 1 if any file would change; --diff shows what would
change without writing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, nil); err != nil {
				return err
			}
			fsys := os.DirFS(a.cfg.Root)
			files := args
			if len(files) == 0 {
				envs, err := a.envs(fsys)
				if err != nil {
					return err
				}
				l := a.cfg.ResolvedLayout()
				for _, env := range envs {
					srcs, err := l.Sources(fsys, env)
					if err != nil {
						a.log.Info("skip " + env + ": " + err.Error())
						continue
					}
					for _, s := range srcs {
						files = append(files, filepath.Join(a.cfg.Root, filepath.FromSlash(s)))
					}
				}
			}
			if len(files) == 0 {
				return kerr.Usagef("no source files found")
			}
			changed, failed := false, false
			for _, f := range files {
				rel := f
				if r, err := filepath.Rel(a.cfg.Root, f); err == nil {
					rel = r
				}
				data, err := os.ReadFile(f)
				if err != nil {
					fmt.Fprintf(a.Stderr, "FAIL: %s (%v)\n", rel, err)
					failed = true
					continue
				}
				m, err := yamlx.Deserialize(data)
				if err != nil {
					fmt.Fprintf(a.Stderr, "FAIL: %s (%v; left untouched)\n", rel, err)
					failed = true
					continue
				}
				out, err := yamlx.Canonical(m)
				if err != nil {
					fmt.Fprintf(a.Stderr, "FAIL: %s (%v)\n", rel, err)
					failed = true
					continue
				}
				if bytes.Equal(data, out) {
					if !a.quiet {
						fmt.Fprintf(a.Stdout, "ok:    %s (already canonical)\n", rel)
					}
					continue
				}
				changed = true
				switch {
				case check:
					fmt.Fprintf(a.Stdout, "DRIFT: %s\n", rel)
				case showDiff:
					fmt.Fprintf(a.Stdout, "DIFF:  %s\n%s", rel, diffx.Indent(diffx.Unified(data, out, "a/"+rel, "b/"+rel), "    "))
				default:
					if backup {
						if err := os.WriteFile(f+".bak", data, 0o644); err != nil {
							return kerr.Usage(err)
						}
					}
					st, err := os.Stat(f)
					if err != nil {
						return kerr.Usage(err)
					}
					if err := os.WriteFile(f, out, st.Mode().Perm()); err != nil {
						return kerr.Usage(err)
					}
					fmt.Fprintf(a.Stdout, "wrote: %s\n", rel)
				}
			}
			if failed {
				return kerr.Usagef("one or more files could not be normalised")
			}
			if changed && (check || showDiff) {
				return kerr.Configf("files are not canonical")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&check, "check", "c", false, "do not write; exit 1 if any file would change")
	cmd.Flags().BoolVarP(&showDiff, "diff", "d", false, "do not write; show the diff")
	cmd.Flags().BoolVarP(&backup, "backup", "b", false, "keep a .bak beside each rewritten file")
	return cmd
}
