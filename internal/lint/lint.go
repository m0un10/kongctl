// Package lint runs a Spectral-style ruleset over a declarative config with
// the vacuum engine, matching `deck file lint`. It is a port of the deck lint
// package (Apache-2.0, Kong Inc.) that works on bytes and never prints.
package lint

import (
	"fmt"
	"os"
	"strings"

	"github.com/daveshanley/vacuum/motor"
	"github.com/daveshanley/vacuum/rulesets"
	sigsyaml "sigs.k8s.io/yaml"
)

// Severity orders lint levels.
type Severity int

const (
	SeverityHint Severity = iota
	SeverityInfo
	SeverityWarn
	SeverityError
)

// ParseSeverity accepts hint, info, warn, error. Unknown values rank as hint.
func ParseSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "error":
		return SeverityError
	case "warn", "warning":
		return SeverityWarn
	case "info":
		return SeverityInfo
	}
	return SeverityHint
}

// Result is one finding.
type Result struct {
	Message  string
	Severity string
	Line     int
	Column   int
	Path     string
}

// Report is the outcome of a lint run.
type Report struct {
	Total   int
	Failing int
	Results []Result
	// Skipped is set when the state file was empty.
	Skipped bool
}

// Failed reports whether any finding reached the fail severity.
func (r *Report) Failed() bool { return r.Failing > 0 }

// String renders findings in decK's plain format.
func (r *Report) String() string {
	if r.Total == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Linting Violations: %d\nFailures: %d\n\n", r.Total, r.Failing)
	for _, v := range r.Results {
		fmt.Fprintf(&b, "[%s][%d:%d] %s\n", v.Severity, v.Line, v.Column, v.Message)
	}
	return b.String()
}

func loadRuleSet(data []byte) (*rulesets.RuleSet, error) {
	rs, err := rulesets.CreateRuleSetFromData(data)
	if err != nil {
		return nil, fmt.Errorf("error creating ruleset: %w", err)
	}
	if len(rs.GetExtendsValue()) > 0 {
		return rulesets.BuildDefaultRuleSets().GenerateRuleSetFromSuppliedRuleSet(rs), nil
	}
	return rs, nil
}

// Lint applies ruleset to spec. failSeverity is the threshold at which a
// finding counts as a failure.
func Lint(spec, ruleset []byte, failSeverity string) (*Report, error) {
	rs, err := loadRuleSet(ruleset)
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := sigsyaml.Unmarshal(spec, &parsed); err == nil && parsed == nil {
		return &Report{Skipped: true}, nil
	}

	// vacuum may write to stdout while executing; stdout belongs to the
	// artifact, so redirect for the duration of the call.
	restore := silenceStdout()
	res := motor.ApplyRulesToRuleSet(&motor.RuleSetExecution{
		RuleSet:           rs,
		Spec:              spec,
		SkipDocumentCheck: true,
		AllowLookup:       true,
		SilenceLogs:       true,
	})
	restore()

	threshold := ParseSeverity(failSeverity)
	rep := &Report{}
	for _, x := range res.Results {
		sev := ""
		if x.Rule != nil {
			sev = x.Rule.Severity
		}
		if sev == "" {
			sev = x.RuleSeverity
		}
		if ParseSeverity(sev) >= threshold {
			rep.Failing++
		}
		rep.Total++
		r := Result{Message: x.Message, Severity: sev}
		if x.Rule != nil {
			if p, ok := x.Rule.Given.(string); ok {
				r.Path = p
			}
		}
		if x.StartNode != nil {
			r.Line, r.Column = x.StartNode.Line, x.StartNode.Column
		}
		rep.Results = append(rep.Results, r)
	}
	return rep, nil
}

func silenceStdout() func() {
	orig := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return func() {}
	}
	os.Stdout = devnull
	return func() {
		os.Stdout = orig
		_ = devnull.Close()
	}
}
