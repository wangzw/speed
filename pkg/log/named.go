package log

import (
	"log/slog"

	"github.com/vislake/speed/pkg/core"
)

// NamedFor takes the logging capability up from reg and returns a logger named after module.
func NamedFor(reg *core.Registry, module string) (*slog.Logger, error) {
	logger, err := core.Resolve[Logger](reg)
	if err != nil {
		return nil, err
	}
	return logger.Named(module), nil
}
