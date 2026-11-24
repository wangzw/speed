package config

import (
	"io"
	"os"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/vislake/speed/pkg/core"
)

const diagnosticPrefix = "config: "

type loader struct {
	reg *core.Registry
	m   *manifest
	t   *transports
	v   *viper.Viper
	// env is the process environment as it stood when loading began; every read in a run uses this one snapshot.
	env         map[string]string
	bound       map[string]*pflag.Flag
	readPrimary bool
	primary     map[string]any
	defaults    map[string]any
	stderr      io.Writer
}

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

func (l *loader) seedDefaults() {
	for _, item := range l.m.items {
		l.v.SetDefault(item.path, item.def)
	}
}

func (l *loader) Manifest() *manifest { return l.m }

func (l *loader) Host() HostIdentity { return l.m.host }

func (l *loader) Reader() Reader {
	modules := l.reg.Modules()
	running := make([]string, len(modules))
	for i, m := range modules {
		running[i] = m.Name
	}
	return l.ReaderFor(running)
}

func (l *loader) ReaderFor(running []string) Reader {
	judged := make(map[string]bool, len(running))
	for _, name := range running {
		judged[name] = true
	}
	return &reader{l: l, judged: judged}
}
