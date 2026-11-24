package config

import (
	"io"
	"os"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/vislake/speed/pkg/core"
)

// diagnosticPrefix opens every diagnostic line this module writes. Startup
// diagnostics have several writers and all of them go to os.Stderr, which a
// host cannot redirect, so each line leads with the party that produced it.
const diagnosticPrefix = "config: "

// loader carries one run's load from the first declaration to the reader.
type loader struct {
	reg *core.Registry
	m   *manifest
	t   *transports
	// v is the layering tool holding the defaults, the primary source and the
	// command line.
	v *viper.Viper
	// env is the process environment as it stood when loading began.
	env map[string]string
	// bound is the argument each item was registered as, by path.
	bound map[string]*pflag.Flag
	// readPrimary records that the primary source has been read.
	readPrimary bool
	// primary and defaults are the two documents before the layering tool
	// folded their keys; a container leaf is read back from here.
	primary  map[string]any
	defaults map[string]any
	stderr   io.Writer
}

// newLoader collects the declarations of a registry and lays the base layer.
// Collection covers every registered module, whichever command this run selects.
func newLoader(reg *core.Registry, stderr io.Writer) (*loader, error) {
	m, err := newManifest(reg)
	if err != nil {
		return nil, err
	}
	t, err := collectTransports(reg)
	if err != nil {
		return nil, err
	}
	d, dModule, hasDefaults, err := collectDefaults(reg)
	if err != nil {
		return nil, err
	}
	l := &loader{
		reg:    reg,
		m:      m,
		t:      t,
		v:      viper.New(),
		env:    environMap(os.Environ()),
		bound:  make(map[string]*pflag.Flag),
		stderr: stderr,
	}
	if hasDefaults {
		if err := l.applyDefaults(d, dModule); err != nil {
			return nil, err
		}
	}
	l.seedDefaults()
	return l, nil
}

// seedDefaults lays the base layer: every declared leaf takes the current value
// of its field in the carrier struct.
func (l *loader) seedDefaults() {
	for _, item := range l.m.items {
		l.v.SetDefault(item.path, item.def)
	}
}

// Manifest is the collected declarations, which is what the command tree
// reads to decide the arguments of a level.
func (l *loader) Manifest() *manifest { return l.m }

// Host is the declared host identity, zero when no module declared one.
func (l *loader) Host() HostIdentity { return l.m.host }

// Reader hands out the values of a run every registered module takes part in.
func (l *loader) Reader() Reader {
	modules := l.reg.Modules()
	running := make([]string, len(modules))
	for i, m := range modules {
		running[i] = m.Name
	}
	return l.ReaderFor(running)
}

// ReaderFor hands out the values of a run that takes the named modules in at run
// level. Their required items are judged and no other module's are.
func (l *loader) ReaderFor(running []string) Reader {
	judged := make(map[string]bool, len(running))
	for _, name := range running {
		judged[name] = true
	}
	return &reader{l: l, judged: judged}
}
