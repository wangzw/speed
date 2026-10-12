package aliyun

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// registryWithReader builds a registry with a real configuration reader over a
// body, supplied under the config module's name, which is what a module that
// reads configuration needs before its Prepare runs.
func registryWithReader(t *testing.T, body string) *core.Registry {
	t.Helper()
	reader := authntest.ConfigReader(t, Module(), body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// completeBody is the configuration body a working assembly is built from.
func completeBody() string {
	return `{"authn":{"sms":{"aliyun":{` +
		`"access-key-id":"the-id","access-key-secret":"the-secret","sign-name":"Speed",` +
		`"templates":` + templatesJSON() + `}}}}`
}

// TestTheDescriptorDeliversTheSenderExclusively holds the shape the design names:
// SMSSender claimed for this module alone, so a command names exactly one
// carrier, and the logging capability taken up optionally.
func TestTheDescriptorDeliversTheSenderExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the sending seam alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.SMSSender)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want an exclusive claim on authn.SMSSender", p)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the one optional logging capability", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want an optional authn-less logging capability", r)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the one input-item schema", m.Resources)
	}
}

// TestPrepareStatesEnabledForARunnableConfiguration holds that a covered
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

// TestPrepareRefusesATemplateMapThatMissesACombination holds the startup gate:
// an incomplete template map stops the assembly before anything is constructed.
func TestPrepareRefusesATemplateMapThatMissesACombination(t *testing.T) {
	short := validTemplates()
	delete(short, "mfa/en-US")
	body := `{"authn":{"sms":{"aliyun":{` +
		`"access-key-id":"the-id","access-key-secret":"the-secret","sign-name":"Speed",` +
		`"templates":` + encodeTemplates(short) + `}}}}`
	reg := registryWithReader(t, body)
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("Prepare gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheModuleDeliversTheSenderThroughTheRegistry drives a real registry through
// this module's descriptor to Init, resolves the sending seam from the product and
// names it.
func TestTheModuleDeliversTheSenderThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, completeBody())
	reg.Register(Module())

	var delivered authn.SMSSender
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			delivered, err = core.Resolve[authn.SMSSender](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if delivered == nil {
		t.Fatal("the registry resolved no sender")
	}
	if delivered.Name() != "aliyun" {
		t.Errorf("Name() = %q, want aliyun", delivered.Name())
	}
}

// encodeTemplates writes a template map as JSON, for a body a case configures.
func encodeTemplates(templates map[string]string) string {
	var b []byte
	b = append(b, '{')
	first := true
	for key, code := range templates {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = append(b, ('"'))
		b = append(b, key...)
		b = append(b, '"', ':', '"')
		b = append(b, code...)
		b = append(b, '"')
	}
	b = append(b, '}')
	return string(b)
}
