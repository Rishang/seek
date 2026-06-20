// Package logx is seek's small leveled logger. It writes human-readable lines
// to stderr (so stdout stays clean for piping results) at a level controlled by
// the SEEK_LOG environment variable: debug, warn (default), error, off.
package logx

import (
	"fmt"
	"os"
	"strings"
)

// Level is a logging severity.
type Level int

const (
	LevelDebug Level = iota
	LevelWarn
	LevelError
	LevelOff
)

var current = LevelWarn

func init() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SEEK_LOG"))) {
	case "debug":
		current = LevelDebug
	case "warn", "warning", "":
		current = LevelWarn
	case "error":
		current = LevelError
	case "off", "none", "silent":
		current = LevelOff
	}
}

// SetLevel overrides the active level (mainly for tests).
func SetLevel(l Level) { current = l }

func logf(l Level, label, format string, args ...any) {
	if l < current {
		return
	}
	fmt.Fprintf(os.Stderr, "seek: %s: %s\n", label, fmt.Sprintf(format, args...))
}

// Debug logs at debug level.
func Debug(format string, args ...any) { logf(LevelDebug, "debug", format, args...) }

// Warn logs at warn level.
func Warn(format string, args ...any) { logf(LevelWarn, "warning", format, args...) }

// Error logs at error level.
func Error(format string, args ...any) { logf(LevelError, "error", format, args...) }
