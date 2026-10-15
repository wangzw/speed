package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// stubName is the module name the cases register the stub carrier under.
const stubName = "mail.stub"

// stubConfig is a carrier configuration that accepts nothing.
type stubConfig struct{}

// stubSender is a carrier that answers the seam and does nothing.
type stubSender struct{}

func (stubSender) Name() string { return "stub" }

func (stubSender) Send(context.Context, Message) (Receipt, error) { return Receipt{}, nil }

// validSpec is self-consistent; a case blanks one field of it.
func validSpec() Spec[stubConfig] {
	prototype := stubConfig{}
	return Spec[stubConfig]{
		ModuleName:      stubName,
		ConfigNamespace: stubName,
		Config:          &prototype,
		Stance: func(*stubConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(context.Context, *stubConfig) (Sender, error) { return stubSender{}, nil },
	}
}

// stubModule is the descriptor the stub carrier registers.
func stubModule() core.Module { return NewModule(validSpec()) }

// TestNewModulePanicsOnADefectiveSpec holds that a Spec missing any part it
// cannot build a module without panics at registration, naming the part.
func TestNewModulePanicsOnADefectiveSpec(t *testing.T) {
	cases := map[string]struct {
		blank    func(*Spec[stubConfig])
		mentions string
	}{
		"no module name": {func(s *Spec[stubConfig]) { s.ModuleName = "" }, "carries the module name"},
		"no namespace":   {func(s *Spec[stubConfig]) { s.ConfigNamespace = "" }, "no configuration namespace"},
		"no config":      {func(s *Spec[stubConfig]) { s.Config = nil }, "no configuration prototype"},
		"no stance":      {func(s *Spec[stubConfig]) { s.Stance = nil }, "no stance"},
		"no open":        {func(s *Spec[stubConfig]) { s.Open = nil }, "no constructor"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			spec := validSpec()
			c.blank(&spec)
			assertPanics(t, c.mentions, func() { NewModule(spec) })
		})
	}
}

// TestTheDescriptorClaimsTheSenderExclusively holds the shape the module
// factory builds: the mail.Sender claimed for this module alone, so a command
// names exactly one carrier, and the logging capability taken up optionally.
func TestTheDescriptorClaimsTheSenderExclusively(t *testing.T) {
	m := NewModule(validSpec())
	if m.Name != stubName {
		t.Errorf("Name = %q, want %q", m.Name, stubName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the sending seam alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*Sender)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want an exclusive claim on mail.Sender", p)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the one optional logging capability", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want an optional logging capability", r)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the one input-item schema", m.Resources)
	}
}

// assertPanics fails the case unless fn panics with a string naming mentions.
func assertPanics(t *testing.T, mentions string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("no panic, want one naming %q", mentions)
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, mentions) {
			t.Errorf("the panic %v does not name %q", r, mentions)
		}
	}()
	fn()
}
