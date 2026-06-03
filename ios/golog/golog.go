// Package golog is go-ios's internal logging seam. Every go-ios module logs
// through these functions instead of importing a logging library directly.
//
// By default it delegates to slog.Default(), so an importer that does nothing
// sees standard slog behavior. Call ios.SetLogger (which forwards to SetLogger
// here) to route go-ios's logs to your own *slog.Logger — this affects only
// go-ios and never touches the process-global slog.Default().
package golog

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
)

// LevelTrace is below slog.LevelDebug, used for the most verbose go-ios logs
// (the CLI's --trace flag). slog has no native trace level.
const LevelTrace slog.Level = slog.LevelDebug - 4

// logger holds the go-ios-scoped logger. Nil means "use slog.Default()", so the
// zero state behaves like plain slog without us caching a logger at init time.
var logger atomic.Pointer[slog.Logger]

// SetLogger routes all go-ios logging to l. Passing nil restores the default
// (slog.Default()). Safe to call concurrently.
func SetLogger(l *slog.Logger) { logger.Store(l) }

// L returns the logger go-ios currently logs through.
func L() *slog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

func Trace(msg string, args ...any) { L().Log(context.Background(), LevelTrace, msg, args...) }
func Debug(msg string, args ...any) { L().Debug(msg, args...) }
func Info(msg string, args ...any)  { L().Info(msg, args...) }
func Warn(msg string, args ...any)  { L().Warn(msg, args...) }
func Error(msg string, args ...any) { L().Error(msg, args...) }

// Fatal logs at error level and exits the process with status 1, matching the
// behavior of the logrus Fatal calls this replaces.
func Fatal(msg string, args ...any) {
	L().Error(msg, args...)
	os.Exit(1)
}

// Enabled reports whether the current logger would emit at the given level.
// Useful to guard expensive log-argument construction.
func Enabled(level slog.Level) bool {
	return L().Enabled(context.Background(), level)
}
