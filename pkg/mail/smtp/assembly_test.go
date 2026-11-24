package smtp

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
)

func registryWithReader(t *testing.T, document string) *core.Registry {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// TestTheDescriptorDeliversTheSenderExclusively checks that the descriptor claims
// mail.Sender exclusively and takes up logging optionally.
func TestTheDescriptorDeliversTheSenderExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the sending seam alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*mail.Sender)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want an exclusive claim on mail.Sender", p)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the one optional logging capability", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want an optional logging capability", r)
	}
	if len(m.Resources) != 2 {
		t.Errorf("Resources = %v, want the input-item schema and the carrier name", m.Resources)
	}
}

// TestPrepareStatesEnabledForARunnableConfiguration checks that a sound
// configuration makes the module state it runs.
func TestPrepareStatesEnabledForARunnableConfiguration(t *testing.T) {
	reg := registryWithReader(t, body(t, section(nil)))
	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestPrepareRefusesAnUnsoundConfiguration checks that a section the carrier
// cannot run on fails Prepare.
func TestPrepareRefusesAnUnsoundConfiguration(t *testing.T) {
	reg := registryWithReader(t, body(t, section(map[string]any{"tls": "plaintext"})))
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("Prepare gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheModuleDeliversTheSenderThroughTheRegistry checks that the registry
// resolves the carrier's sender and names it.
func TestTheModuleDeliversTheSenderThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, body(t, section(nil)))
	reg.Register(Module())

	var delivered mail.Sender
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			delivered, err = core.Resolve[mail.Sender](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if delivered == nil {
		t.Fatal("the registry resolved no sender")
	}
	if delivered.Name() != carrierName {
		t.Errorf("Name() = %q, want %q", delivered.Name(), carrierName)
	}
}
