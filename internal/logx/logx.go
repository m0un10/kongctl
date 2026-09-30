// Package logx is the one logger kongctl uses. Everything it writes goes to
// the writer it was given (stderr in the CLI), never to stdout, because stdout
// is reserved for the artifact a command produces.
package logx

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Level is a log threshold.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel accepts debug, info, warn, error (case-insensitive).
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, nil
	case "info", "":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("unknown log level %q (debug, info, warn, error)", s)
}

// Logger writes prefixed single-line messages.
type Logger struct {
	mu     sync.Mutex
	w      io.Writer
	level  Level
	prefix string
}

// New returns a logger writing to w at the given level. The prefix is placed
// before every line, for example "[kongctl]".
func New(w io.Writer, level Level, prefix string) *Logger {
	return &Logger{w: w, level: level, prefix: prefix}
}

// Discard returns a logger that drops everything.
func Discard() *Logger { return New(io.Discard, LevelError+1, "") }

// Level returns the current threshold.
func (l *Logger) Level() Level { return l.level }

// Prefix returns the line prefix.
func (l *Logger) Prefix() string { return l.prefix }

// Writer returns the underlying writer.
func (l *Logger) Writer() io.Writer { return l.w }

// Child returns a logger with the same level and prefix writing to w. It is
// how check buffers one environment's output to replay it under a heading.
func (l *Logger) Child(w io.Writer) *Logger {
	return New(w, l.level, l.prefix)
}

// Capture returns a logger that buffers into buf with the same settings.
func (l *Logger) Capture(buf *bytes.Buffer) *Logger { return l.Child(buf) }

func (l *Logger) log(level Level, tag, msg string, kv []any) {
	if level < l.level {
		return
	}
	var b strings.Builder
	if l.prefix != "" {
		b.WriteString(l.prefix)
		b.WriteByte(' ')
	}
	if tag != "" {
		b.WriteString(tag)
		b.WriteString(": ")
	}
	b.WriteString(msg)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
	}
	b.WriteByte('\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.w, b.String())
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, kv ...any) { l.log(LevelDebug, "", msg, kv) }

// Info logs at info level.
func (l *Logger) Info(msg string, kv ...any) { l.log(LevelInfo, "", msg, kv) }

// Warn logs at warn level with a WARNING tag.
func (l *Logger) Warn(msg string, kv ...any) { l.log(LevelWarn, "WARNING", msg, kv) }

// Error logs at error level with an ERROR tag.
func (l *Logger) Error(msg string, kv ...any) { l.log(LevelError, "ERROR", msg, kv) }

// Raw writes lines verbatim (used to relay tool output such as lint findings),
// honouring the level threshold at info.
func (l *Logger) Raw(text string) {
	if LevelInfo < l.level || text == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	_, _ = io.WriteString(l.w, text)
}
