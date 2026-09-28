package config

import (
	"io"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/vislake/speed/pkg/core"
)

// diagnosticPrefix opens every diagnostic line this module writes. Startup
// diagnostics have several writers and all of them go to os.Stderr, which a
// host cannot redirect, so each line leads with the party that produced it.
const diagnosticPrefix = "config: "

// loader carries one run's load from the first declaration to the reader.
//
// It is staged rather than done in one call because the command tree descends
// one level at a time: which arguments exist on a level depends on which
// modules that level takes in, and which level comes next is the answer the
// previous one parsed. Collection is not staged: it happens once, over every
// registered module, before any level is looked at.
type loader struct {
	reg *core.Registry
	m   *manifest
	t   *transports
	// v is the layering tool holding the defaults, the primary source, the
	// environment and the command line.
	v *viper.Viper
	// bound is the argument each item was registered as, by path. It is the
	// command-line leg of the question which layer gave a value, and it
	// fills up as the command tree descends.
	bound map[string]*pflag.Flag
	// readPrimary records that the primary source has been read, so that a
	// second read is caught rather than merged on top of the first.
	readPrimary bool
	// primary is what the primary config source wrote, before the layering
	// tool folded its keys. A container leaf is read back from here: its
	// inner keys are the user's own data and are not in the manifest, so
	// folding them changes the configuration rather than normalising a name.
	primary map[string]any
	stderr  io.Writer
}

// newLoader collects the declarations of a registry and lays the base layer.
//
// Collection comes before everything and covers every registered module,
// whichever command this run selects: without the whole manifest there is no
// way to tell an unknown key from a misspelled one, and no way to know which
// environment variable names are claimed.
//
// It is not exported. The stages have an order the reader depends on -- a
// Reader taken before the command line is bound reports every required item
// given there as missing -- and App and Load are the two places that keep it,
// so they are the two ways in.
func newLoader(reg *core.Registry, stderr io.Writer) (*loader, error) {
	m, err := newManifest(reg)
	if err != nil {
		return nil, err
	}
	t, err := collectTransports(reg)
	if err != nil {
		return nil, err
	}
	l := &loader{
		reg:    reg,
		m:      m,
		t:      t,
		v:      viper.New(),
		bound:  make(map[string]*pflag.Flag),
		stderr: stderr,
	}
	l.seedDefaults()
	if err := l.bindEnv(); err != nil {
		return nil, err
	}
	return l, nil
}

// seedDefaults lays the base layer: every declared leaf takes the current
// value of its field in the carrier struct. Decode reads from the layering
// tool alone and never goes back to the prototype, which is how a caller
// passing a zero struct still gets the declared defaults.
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

// Reader hands out the values of a run every registered module takes part in,
// which is the run Load assembles: the required items of every module are
// judged.
//
// The command line leg of the question which layer gave a value rests on
// BindLayer having been called for every level of the selected command path.
// Handing out a reader before that would make the leg answer no for every
// item, and a required item given on the command line would be reported as
// missing.
func (l *loader) Reader() Reader {
	modules := l.reg.Modules()
	running := make([]string, len(modules))
	for i, m := range modules {
		running[i] = m.Name
	}
	return l.ReaderFor(running)
}

// ReaderFor hands out the values of a run that takes the named modules in at
// run level. Their required items are judged and no other module's are: a
// module the run only collects from is never constructed, so nothing of the
// run is short of its values, and a command reading them in its own action is
// reporting on them rather than depending on them.
func (l *loader) ReaderFor(running []string) Reader {
	judged := make(map[string]bool, len(running))
	for _, name := range running {
		judged[name] = true
	}
	return &reader{l: l, judged: judged}
}
