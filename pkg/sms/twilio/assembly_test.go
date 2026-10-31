package twilio

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
)

// registryWithReader builds a registry with a real configuration reader over a
// body, supplied under the config module's name, which is what a module that
// reads configuration needs before its Prepare runs.
func registryWithReader(t *testing.T, body string) *core.Registry {
	t.Helper()
	reader := configtest.Reader(t, body, Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// TestTheDescriptorDeliversBothSeamsExclusively holds the shape the design names:
// the sms.Sender and sms.Provider claimed for this module alone, so a command
// names exactly one carrier, and the logging capability taken up optionally.
func TestTheDescriptorDeliversBothSeamsExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	senderToken := core.Token((*sms.Sender)(nil))
	providerToken := core.Token((*sms.Provider)(nil))
	var sawSender, sawProvider bool
	for _, p := range m.Provides {
		if !p.Exclusive {
			t.Errorf("Provides entry %+v is not exclusive", p)
		}
		switch p.Token {
		case senderToken:
			sawSender = true
		case providerToken:
			sawProvider = true
		default:
			t.Errorf("Provides entry %+v is not one of the two carrier seams", p)
		}
	}
	if len(m.Provides) != 2 || !sawSender || !sawProvider {
		t.Errorf("Provides = %+v, want the sending and the management seam, exclusively", m.Provides)
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

// TestPrepareStatesEnabledForARunnableConfiguration holds that a complete
// configuration lets the module state it runs.
func TestPrepareStatesEnabledForARunnableConfiguration(t *testing.T) {
	reg := registryWithReader(t, completeBody())
	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestTheModuleDeliversBothSeamsThroughTheRegistry drives a real registry through
// this module's descriptor to Init, resolves both carrier seams from the one
// product and names it.
func TestTheModuleDeliversBothSeamsThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, completeBody())
	reg.Register(Module())

	var sender sms.Sender
	var provider sms.Provider
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			if sender, err = core.Resolve[sms.Sender](reg); err != nil {
				return err
			}
			provider, err = core.Resolve[sms.Provider](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if sender == nil || sender.Name() != "twilio" {
		t.Errorf("the sending seam resolved %v, want one named twilio", sender)
	}
	if provider == nil || provider.Name() != "twilio" {
		t.Errorf("the management seam resolved %v, want one named twilio", provider)
	}
	if provider != nil && provider.Model().Identities[0].Name != "service" {
		t.Errorf("the management seam declares %+v, want the configured identity", provider.Model().Identities)
	}
}
