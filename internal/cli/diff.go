package cli

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/diffx"
	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/note"
	"github.com/m0un10/kongctl/internal/note/gitlab"
	"github.com/m0un10/kongctl/internal/review"
)

type diffFlags struct {
	base, rev, style string
	noMergeBase      bool
}

func (f *diffFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVarP(&f.base, "base", "B", "", "base ref to compare against (default: diff.baseRef, origin/main)")
	fl.StringVar(&f.rev, "rev", "", "compare this ref instead of the working tree")
	fl.BoolVar(&f.noMergeBase, "no-merge-base", false, "compare against the base tip, not merge-base(base, HEAD)")
}

// runReview resolves the base and compares every environment.
func (a *App) runReview(ctx context.Context, f *diffFlags) (results []review.Result, baseLabel string, headSHA string, err error) {
	g := a.git()
	base := a.cfg.Diff.BaseRef
	if f.base != "" {
		base = f.base
	}
	useMB := a.cfg.UseMergeBase() && !f.noMergeBase
	baseSHA, fellBack, err := gitx.ResolveBase(ctx, g, base, useMB)
	if err != nil {
		return nil, "", "", kerr.Usagef("base ref %q not found (fetch it, or pass --base)", base)
	}
	if fellBack {
		a.log.Warn(fmt.Sprintf("no merge-base with %s (shallow clone?); using its tip instead", base))
	}
	var headFS fs.FS = os.DirFS(a.cfg.Root)
	if f.rev != "" {
		sha, err := g.RevParse(ctx, f.rev)
		if err != nil {
			return nil, "", "", kerr.Usagef("rev %q not found", f.rev)
		}
		headFS = gitx.NewFS(ctx, g, sha)
		headSHA = sha
	} else if sha, err := g.RevParse(ctx, "HEAD"); err == nil {
		headSHA = sha
	}
	envs, err := a.envs(headFS)
	if err != nil {
		return nil, "", "", err
	}
	if len(envs) == 0 {
		// The environment may only exist on the base side.
		envs, _ = a.cfg.ResolvedLayout().DiscoverEnvs(gitx.NewFS(ctx, g, baseSHA))
	}
	l := a.cfg.ResolvedLayout()
	for _, env := range envs {
		results = append(results, review.Compare(ctx, g, l, env, review.Options{Base: baseSHA, HeadFS: headFS}))
	}
	short := baseSHA
	if len(short) > 7 {
		short = short[:7]
	}
	return results, fmt.Sprintf("%s (%s)", base, short), headSHA, nil
}

func (a *App) diffCmd() *cobra.Command {
	var f diffFlags
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Diff each environment's composed config against a git base",
		Long: `Both sides are composed before comparison, so splitting or reordering
files shows no semantic change while a real change shows once. The semantic
view sorts keys and normalises quoting; --raw shows the source files as
committed. Exit 1 when differences are found, 2 on error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, nil); err != nil {
				return err
			}
			results, baseLabel, headSHA, err := a.runReview(cmd.Context(), &f)
			if err != nil {
				return err
			}
			head := "working tree"
			if f.rev != "" {
				head = f.rev
			}
			if !a.quiet {
				fmt.Fprintf(a.Stdout, "base: %s\nhead: %s\n\n", baseLabel, head)
			}
			_ = headSHA
			differences, failed := false, false
			for _, r := range results {
				switch r.Status {
				case review.StatusError:
					failed = true
					fmt.Fprintf(a.Stderr, "FAIL:    %s (%v)\n", r.Env, r.Err)
					continue
				case review.StatusAbsent:
					if !a.quiet {
						fmt.Fprintf(a.Stdout, "skip:    %s (absent on both sides)\n", r.Env)
					}
					continue
				case review.StatusAdded:
					differences = true
					fmt.Fprintf(a.Stdout, "ADDED:   %s (new on this branch)\n", r.Env)
					continue
				case review.StatusDeleted:
					differences = true
					fmt.Fprintf(a.Stdout, "DELETED: %s (removed on this branch)\n", r.Env)
					continue
				}
				if f.style != "raw" {
					if r.SemanticChanged() {
						differences = true
						fmt.Fprintf(a.Stdout, "DIFF:    %s (semantic)\n%s", r.Env, diffx.Indent(r.Semantic, "    "))
					} else if !a.quiet {
						fmt.Fprintf(a.Stdout, "ok:      %s (no semantic change)\n", r.Env)
						if r.RawChanged() {
							fmt.Fprintln(a.Stdout, "         note: formatting differs, see --raw")
						}
					}
				}
				if f.style != "semantic" {
					if r.RawChanged() {
						differences = true
						fmt.Fprintf(a.Stdout, "DIFF:    %s (raw)\n%s", r.Env, diffx.Indent(r.Raw, "    "))
					} else if !a.quiet {
						fmt.Fprintf(a.Stdout, "ok:      %s (sources identical)\n", r.Env)
					}
				}
			}
			if failed {
				return kerr.Usagef("diff failed for one or more environments")
			}
			if differences {
				return kerr.Configf("differences found")
			}
			return nil
		},
	}
	f.bind(cmd)
	fl := cmd.Flags()
	f.style = "semantic"
	var raw, both, semantic bool
	fl.BoolVarP(&semantic, "semantic", "s", false, "semantic diff only (default)")
	fl.BoolVarP(&raw, "raw", "R", false, "raw diff of the source files only")
	fl.BoolVar(&both, "both", false, "semantic then raw")
	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		switch {
		case both:
			f.style = "both"
		case raw:
			f.style = "raw"
		default:
			f.style = "semantic"
		}
	}
	return cmd
}

func (a *App) notateCmd() *cobra.Command {
	var f diffFlags
	var post bool
	cmd := &cobra.Command{
		Use:   "notate",
		Short: "Build the merge-request note summarising the config diff",
		Long: `Prints the note body. With --post the note is created on the merge
request, or updated in place when one carrying the marker already exists.
GitLab settings come from CI_API_V4_URL, CI_PROJECT_ID, CI_MERGE_REQUEST_IID
and GITLAB_TOKEN (or the gitlab section of the config file).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.setup(cmd, func(c *config.Config) {
				if v, ok := a.Lookup("CI_MERGE_REQUEST_TARGET_BRANCH_NAME"); ok && v != "" && f.base == "" {
					if _, set := a.Lookup("BASE_REF"); !set {
						if _, set := a.Lookup("KONGCTL_DIFF_BASE_REF"); !set {
							c.Diff.BaseRef = "origin/" + v
						}
					}
				}
			}); err != nil {
				return err
			}
			results, baseLabel, headSHA, err := a.runReview(cmd.Context(), &f)
			if err != nil {
				return err
			}
			short := headSHA
			if len(short) > 8 {
				short = short[:8]
			}
			body := note.Build(results, note.Options{
				Marker:   a.cfg.Note.Marker,
				MaxLines: a.cfg.Note.MaxLines,
				BaseRef:  baseLabel,
				HeadSHA:  short,
				RunHint:  "kongctl diff --both",
			})
			if !post {
				fmt.Fprint(a.Stdout, body)
				return nil
			}
			if a.cfg.Note.Provider != "gitlab" {
				return kerr.Usagef("note.provider %q is not supported (gitlab)", a.cfg.Note.Provider)
			}
			gl := a.cfg.GitLab
			client, err := gitlab.New(http.DefaultClient, gl.BaseURL, gl.ProjectID, gl.MRIID, gl.Token)
			if err != nil {
				return kerr.Usage(err)
			}
			action, lookupErr, err := note.Post(cmd.Context(), client, a.cfg.Note.Marker, body)
			if lookupErr != nil {
				a.log.Warn("note lookup failed, a new note will be created: " + lookupErr.Error())
			}
			if err != nil {
				return kerr.Usage(err)
			}
			a.log.Info(action + " note")
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&post, "post", false, "create or update the note on the merge request")
	return cmd
}
