// Package placeholder finds and substitutes ${NAME} references in a
// JSON-shaped configuration tree.
//
// Substitution is literal text replacement: a value is copied verbatim, never
// interpreted as a regex template or run through envsubst. Two modes exist.
// Embedded (the default) accepts a placeholder anywhere inside a string.
// Whole requires the entire scalar to be one placeholder, which is the
// stricter rule the original bash tooling enforced.
//
// In both modes a string that contains the opening token but no well-formed
// placeholder is an offender and fails the run, so a typo like ${host} or an
// unclosed ${NAME can never ship literally.
package placeholder

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Mode selects where a placeholder may appear.
type Mode string

const (
	ModeEmbedded Mode = "embedded"
	ModeWhole    Mode = "whole"
)

// Syntax describes the placeholder tokens.
type Syntax struct {
	Mode   Mode
	Open   string
	Close  string
	NameRE *regexp.Regexp
}

// DefaultSyntax is ${NAME} anywhere, upper snake case names.
func DefaultSyntax() Syntax {
	return Syntax{Mode: ModeEmbedded, Open: "${", Close: "}", NameRE: regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)}
}

// Occurrence is one placeholder reference.
type Occurrence struct {
	Name string
	Path string
	// Whole is true when the placeholder is the entire scalar.
	Whole bool
}

// Offender is a string that mentions the opening token without forming a
// valid placeholder.
type Offender struct {
	Path   string
	Value  string
	Reason string
}

// Report is the outcome of a scan.
type Report struct {
	// Names is the sorted, de-duplicated list of placeholder names.
	Names       []string
	Occurrences []Occurrence
	Offenders   []Offender
}

// token is one piece of a tokenised string.
type token struct {
	literal string // set for text
	name    string // set for a placeholder
}

// tokenize splits s into literals and placeholders. It returns an error
// message when the string is malformed.
func (s Syntax) tokenize(str string) ([]token, string) {
	var toks []token
	rest := str
	for {
		i := strings.Index(rest, s.Open)
		if i < 0 {
			toks = append(toks, token{literal: rest})
			return toks, ""
		}
		// "$${" is an escaped literal in embedded mode.
		if s.Mode == ModeEmbedded && i > 0 && strings.HasSuffix(rest[:i], "$") && strings.HasPrefix(s.Open, "$") {
			toks = append(toks, token{literal: rest[:i-1] + s.Open})
			rest = rest[i+len(s.Open):]
			continue
		}
		toks = append(toks, token{literal: rest[:i]})
		after := rest[i+len(s.Open):]
		j := strings.Index(after, s.Close)
		if j < 0 {
			return nil, fmt.Sprintf("unclosed placeholder starting at %q", s.Open+truncate(after, 20))
		}
		name := after[:j]
		if !s.NameRE.MatchString(name) {
			return nil, fmt.Sprintf("invalid placeholder name %q (must match %s)", name, s.NameRE)
		}
		toks = append(toks, token{name: name})
		rest = after[j+len(s.Close):]
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// inspect classifies one string.
func (s Syntax) inspect(path, str string) (names []string, whole bool, off *Offender) {
	if !strings.Contains(str, s.Open) {
		return nil, false, nil
	}
	toks, problem := s.tokenize(str)
	if problem != "" {
		return nil, false, &Offender{Path: path, Value: str, Reason: problem}
	}
	for _, t := range toks {
		if t.name != "" {
			names = append(names, t.name)
		}
	}
	if len(names) == 0 {
		// only escaped literals
		return nil, false, nil
	}
	whole = len(toks) == 3 && toks[0].literal == "" && toks[2].literal == "" && toks[1].name != "" ||
		len(toks) == 2 && toks[0].literal == "" && toks[1].name != ""
	if s.Mode == ModeWhole && !whole {
		return nil, false, &Offender{Path: path, Value: str, Reason: "placeholders must be the whole value in whole mode, e.g. key: " + s.Open + "NAME" + s.Close}
	}
	return names, whole, nil
}

// Scan walks the tree and reports every placeholder and every offender.
func Scan(v any, s Syntax) Report {
	var rep Report
	seen := map[string]bool{}
	walk(v, "$", func(path string, str string, isKey bool) {
		if isKey {
			if strings.Contains(str, s.Open) {
				rep.Offenders = append(rep.Offenders, Offender{Path: path, Value: str, Reason: "placeholders are not allowed in map keys"})
			}
			return
		}
		names, whole, off := s.inspect(path, str)
		if off != nil {
			rep.Offenders = append(rep.Offenders, *off)
			return
		}
		for _, n := range names {
			rep.Occurrences = append(rep.Occurrences, Occurrence{Name: n, Path: path, Whole: whole})
			if !seen[n] {
				seen[n] = true
				rep.Names = append(rep.Names, n)
			}
		}
	})
	sort.Strings(rep.Names)
	return rep
}

// OffenderError formats offenders as one error listing every value.
func OffenderError(offs []Offender) error {
	if len(offs) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("malformed placeholders:")
	for _, o := range offs {
		fmt.Fprintf(&b, "\n    %s: %q (%s)", o.Path, o.Value, o.Reason)
	}
	return errors.New(b.String())
}

// Lookup resolves a placeholder name to a value.
type Lookup func(name string) (string, bool)

// RenderOptions controls Render.
type RenderOptions struct {
	// Fake substitutes FakePrefix+NAME for any unset name instead of failing.
	Fake       bool
	FakePrefix string
}

// RenderReport records what Render did.
type RenderReport struct {
	Substituted []string
	Missing     []string
	Faked       []string
}

// Render returns a copy of the tree with every placeholder replaced. Empty
// values count as missing. All missing names are reported together.
func Render(v any, s Syntax, lookup Lookup, opts RenderOptions) (any, RenderReport, error) {
	rep := Scan(v, s)
	if err := OffenderError(rep.Offenders); err != nil {
		return nil, RenderReport{}, err
	}
	values := map[string]string{}
	var report RenderReport
	for _, name := range rep.Names {
		val, ok := lookup(name)
		if !ok || val == "" {
			if opts.Fake {
				val = opts.FakePrefix + name
				report.Faked = append(report.Faked, name)
			} else {
				report.Missing = append(report.Missing, name)
				continue
			}
		}
		values[name] = val
		report.Substituted = append(report.Substituted, name)
	}
	if len(report.Missing) > 0 {
		return nil, report, fmt.Errorf("unset or empty placeholders: %s", strings.Join(report.Missing, " "))
	}
	out := transform(v, func(str string) string {
		if !strings.Contains(str, s.Open) {
			return str
		}
		toks, _ := s.tokenize(str)
		var b strings.Builder
		for _, t := range toks {
			if t.name != "" {
				b.WriteString(values[t.name])
			} else {
				b.WriteString(t.literal)
			}
		}
		return b.String()
	})
	return out, report, nil
}

// Expressions replaces every placeholder with a text produced by expr, which
// receives the name and whether the placeholder is the whole scalar. It is
// how ExternalSecret mode turns ${NAME} into a template expression, and it
// also reports the names so the caller can build the data entries. Strings
// containing the literal sequence escape are rewritten through escapeFn.
func Expressions(v any, s Syntax, expr func(name string, whole bool) string, escapeFn func(string) string) (any, Report, error) {
	rep := Scan(v, s)
	if err := OffenderError(rep.Offenders); err != nil {
		return nil, rep, err
	}
	out := transform(v, func(str string) string {
		if escapeFn != nil {
			str = escapeFn(str)
		}
		if !strings.Contains(str, s.Open) {
			return str
		}
		toks, _ := s.tokenize(str)
		whole := len(toks) == 3 && toks[0].literal == "" && toks[2].literal == "" && toks[1].name != "" ||
			len(toks) == 2 && toks[0].literal == "" && toks[1].name != ""
		var b strings.Builder
		for _, t := range toks {
			if t.name != "" {
				b.WriteString(expr(t.name, whole))
			} else {
				b.WriteString(t.literal)
			}
		}
		return b.String()
	})
	return out, rep, nil
}

// walk visits every string in the tree. isKey marks map keys.
func walk(v any, path string, visit func(path, str string, isKey bool)) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			visit(path, k, true)
			walk(t[k], path+"."+k, visit)
		}
	case []any:
		for i, item := range t {
			walk(item, path+"["+strconv.Itoa(i)+"]", visit)
		}
	case string:
		visit(path, t, false)
	}
}

// transform returns a deep copy with every string passed through fn.
func transform(v any, fn func(string) string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = transform(val, fn)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = transform(item, fn)
		}
		return out
	case string:
		return fn(t)
	default:
		return v
	}
}
