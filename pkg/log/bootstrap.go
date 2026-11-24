package log

import "log/slog"

var bootstrapChain = newBootstrapChain()

var defaultLogger = slog.New(bootstrapChain)

func newBootstrapChain() slog.Handler {
	encoder := slog.NewTextHandler(bootstrapStdoutWriter, &slog.HandlerOptions{Level: allLevels})
	return newLevelHandler(bootstrapLevel, newRedactHandler(encoder))
}

// Default returns the process default logger, for the call sites that have no context to take one
// from.
// It writes to standard output and filters by the bootstrap level, whatever a host configured.
func Default() *slog.Logger { return defaultLogger }
