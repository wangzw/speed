package log

import (
	"log/slog"

	"github.com/vislake/speed/pkg/core"
)

// NamedFor takes the logging capability up from reg and returns a logger named
// after module. It is what a host calls to give a module its logger, gathering
// the resolve-and-name pair each host module used to write for itself, so that
// "a module's logger is named after the module" is one place rather than one
// per host.
//
// It is equivalent to taking the instance up and calling Named on it, and it
// hands back the lookup's error unchanged: a registry with no logging module,
// or one whose module has no instance, gives back core.ErrMissingProvider,
// which errors.Is matches. It adds no dependency edge: a module that logs still
// declares the requirement on Logger itself, and that declaration is what orders
// the logging module ahead of its construction.
func NamedFor(reg *core.Registry, module string) (*slog.Logger, error) {
	logger, err := core.Resolve[Logger](reg)
	if err != nil {
		return nil, err
	}
	return logger.Named(module), nil
}
