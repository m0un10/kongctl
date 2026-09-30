// Package kerr classifies errors into the exit codes kongctl promises:
//
//	0  success
//	1  a problem with the configuration being processed (validation, lint,
//	   missing placeholder values, unparseable source)
//	2  usage or tooling (bad flags, missing files that must exist, git failures)
//
// Only cmd/kongctl/main.go turns an error into an exit code.
package kerr

import (
	"errors"
	"fmt"
)

// Kind is the class of an error, which is also its exit code.
type Kind int

const (
	// KindConfig is a problem with the Kong configuration under test.
	KindConfig Kind = 1
	// KindUsage is a problem with how kongctl was invoked or with the tooling.
	KindUsage Kind = 2
)

// Error carries a Kind alongside the wrapped cause.
type Error struct {
	Kind Kind
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Config wraps err as a configuration problem (exit 1).
func Config(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: KindConfig, Err: err}
}

// Configf formats a configuration problem (exit 1).
func Configf(format string, args ...any) error {
	return &Error{Kind: KindConfig, Err: fmt.Errorf(format, args...)}
}

// Usage wraps err as a usage or tooling problem (exit 2).
func Usage(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: KindUsage, Err: err}
}

// Usagef formats a usage or tooling problem (exit 2).
func Usagef(format string, args ...any) error {
	return &Error{Kind: KindUsage, Err: fmt.Errorf(format, args...)}
}

// ExitCode maps an error to the process exit code. Unclassified errors are
// treated as tooling failures.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ke *Error
	if errors.As(err, &ke) {
		return int(ke.Kind)
	}
	return int(KindUsage)
}

// IsConfig reports whether err is classified as a configuration problem.
func IsConfig(err error) bool {
	var ke *Error
	return errors.As(err, &ke) && ke.Kind == KindConfig
}
