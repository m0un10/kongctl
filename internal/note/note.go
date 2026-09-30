// Package note builds the merge-request note that summarises the Kong config
// diff per environment, and posts it through a Provider so the same note can
// go to GitLab today and another host later.
package note

import (
	"context"
	"fmt"
	"strings"

	"github.com/m0un10/kongctl/internal/review"
)

// Options shape the note.
type Options struct {
	Marker   string
	MaxLines int
	BaseRef  string
	HeadSHA  string
	// RunHint is the command suggested for the full diff.
	RunHint string
}

// Build renders the markdown body from per-environment results.
func Build(results []review.Result, o Options) string {
	var b strings.Builder
	b.WriteString(o.Marker + "\n")
	b.WriteString("### Kong config diff\n\n")

	anyChange, anyError := false, false
	var rows, parts []string
	for _, r := range results {
		env := r.Env
		switch r.Status {
		case review.StatusError:
			anyError = true
			rows = append(rows, fmt.Sprintf("| `%s` | ⚠️ error | — |", env))
			parts = append(parts, section(true, "⚠️ <code>"+env+"</code> — diff failed", errText(r.Err), o))
		case review.StatusAbsent:
			continue
		case review.StatusAdded:
			anyChange = true
			rows = append(rows, fmt.Sprintf("| `%s` | 🆕 added | — |", env))
		case review.StatusDeleted:
			anyChange = true
			rows = append(rows, fmt.Sprintf("| `%s` | 🗑️ deleted | — |", env))
		default:
			if !r.SemanticChanged() && !r.RawChanged() {
				rows = append(rows, fmt.Sprintf("| `%s` | ✅ none | ✅ none |", env))
				continue
			}
			anyChange = true
			if r.SemanticChanged() {
				fmtCell := "✅ none"
				if r.RawChanged() {
					fmtCell = "🎨 changed"
				}
				rows = append(rows, fmt.Sprintf("| `%s` | 🔴 changed | %s |", env, fmtCell))
				parts = append(parts, section(true, "🔴 <code>"+env+"</code> — semantic changes", r.Semantic, o))
				if r.RawChanged() {
					parts = append(parts, section(false, "<code>"+env+"</code> — formatting / raw diff", r.Raw, o))
				}
			} else {
				rows = append(rows, fmt.Sprintf("| `%s` | ✅ none | 🎨 formatting only |", env))
				parts = append(parts, section(true, "🎨 <code>"+env+"</code> — formatting only, no semantic change", r.Raw, o))
			}
		}
	}

	if !anyChange && !anyError {
		fmt.Fprintf(&b, "**No changes.** No semantic or formatting differences against `%s`.\n", o.BaseRef)
		return b.String()
	}
	b.WriteString("| Environment | Semantic | Formatting |\n|---|---|---|\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	b.WriteString("\n")
	for _, p := range parts {
		b.WriteString(p + "\n")
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "<sub>Base `%s`", o.BaseRef)
	if o.HeadSHA != "" {
		fmt.Fprintf(&b, " · head `%s`", o.HeadSHA)
	}
	b.WriteString(" · semantic = composed config with sorted keys; ignores key order, quoting and file layout.")
	b.WriteString(" Formatting = raw diff of the source files.</sub>\n")
	return b.String()
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func section(open bool, summary, content string, o Options) string {
	var b strings.Builder
	if open {
		b.WriteString("<details open>\n")
	} else {
		b.WriteString("<details>\n")
	}
	// The blank line after summary is required for markdown inside HTML.
	fmt.Fprintf(&b, "<summary>%s</summary>\n\n", summary)
	b.WriteString(fence(content, o))
	b.WriteString("\n</details>\n")
	return b.String()
}

func fence(content string, o Options) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var b strings.Builder
	b.WriteString("```diff\n")
	if o.MaxLines > 0 && len(lines) > o.MaxLines {
		b.WriteString(strings.Join(lines[:o.MaxLines], "\n") + "\n")
		hint := o.RunHint
		if hint == "" {
			hint = "kongctl diff"
		}
		fmt.Fprintf(&b, "\n... truncated: %d of %d lines shown. Run `%s` locally for the full diff.\n", o.MaxLines, len(lines), hint)
	} else {
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// Provider creates or updates the single note carrying the marker.
type Provider interface {
	// FindNote returns the id of the note containing marker, if any.
	FindNote(ctx context.Context, marker string) (id string, found bool, err error)
	CreateNote(ctx context.Context, body string) error
	UpdateNote(ctx context.Context, id, body string) error
}

// Post creates or updates the note. A failed lookup degrades to creating a
// new note and returns lookupErr so the caller can warn.
func Post(ctx context.Context, p Provider, marker, body string) (action string, lookupErr error, err error) {
	id, found, ferr := p.FindNote(ctx, marker)
	if ferr != nil {
		if err := p.CreateNote(ctx, body); err != nil {
			return "", ferr, err
		}
		return "created", ferr, nil
	}
	if found {
		if err := p.UpdateNote(ctx, id, body); err != nil {
			return "", nil, err
		}
		return "updated " + id, nil, nil
	}
	if err := p.CreateNote(ctx, body); err != nil {
		return "", nil, err
	}
	return "created", nil, nil
}
