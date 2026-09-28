package config

import (
	"context"
	"io"
	"os"

	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

// Load reads the configuration of a registry without a command tree: every
// registered module takes part, and the arguments are one flat set.
//
// It is the loading step on its own. A host writes a command tree instead, and
// Execute runs this same step across its levels; Load is for a caller that
// wants the values and has no lifecycle to drive, a test of a module outside
// its own package among them.
//
// The environment is the process's own throughout: the <PREFIX>_CONFIG that
// selects the primary source is read from it, and so are the values, which is
// where the layering tool reads them. A caller that steers a variable writes
// it into the process environment, which in a test is t.Setenv.
func Load(ctx context.Context, reg *core.Registry, args []string) (Reader, error) {
	l, err := newLoader(reg, os.Stderr)
	if err != nil {
		return nil, err
	}

	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	locator := fs.String(locatorFlag, "", locatorUsage)

	modules := reg.Modules()
	participants := make([]string, len(modules))
	for i, m := range modules {
		participants[i] = m.Name
	}
	if err := l.BindLayer(fs, participants, participants, nil); err != nil {
		return nil, err
	}
	if err := fs.Parse(args); err != nil {
		return nil, l.ClassifyParseError(err)
	}

	if err := l.ReadPrimary(ctx, locate(l.m, environMap(os.Environ()), *locator, fs.Changed(locatorFlag))); err != nil {
		return nil, err
	}
	l.ReportUnclaimed()
	return l.Reader(), nil
}
