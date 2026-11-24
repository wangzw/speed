package config

import (
	"context"
	"io"
	"os"

	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

// Load reads the configuration of a registry without a command tree: every
// registered module takes part, and the arguments are one flat set. It returns
// the reader; a caller that drives the registry supplies it itself.
//
// The environment is read once when the call begins: a caller that steers a
// variable writes it into the process environment first.
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
	if err := l.BindLayer(fs, participants, participants); err != nil {
		return nil, err
	}
	// Every module runs here, so none is collected from alone.
	if err := fs.Parse(args); err != nil {
		return nil, l.ClassifyParseError(err, nil)
	}

	if err := l.ReadPrimary(ctx, locate(l.m, l.env, *locator, fs.Changed(locatorFlag))); err != nil {
		return nil, err
	}
	l.ReportUnclaimed()
	l.ReportSensitive()
	return l.Reader(), nil
}
