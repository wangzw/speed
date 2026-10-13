package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/internal/configtest"
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

// completeBody is the configuration body a working assembly is built from.
func completeBody() string {
	return `{"sms":{"aliyun":{` +
		`"access-key-id":"the-id","access-key-secret":"the-secret","sign-name":"Speed",` +
		`"templates":` + templatesJSON() + `}}}`
}

// TestTheDescriptorDeliversTheSenderExclusively holds the shape the design names:
// the sms.Sender claimed for this module alone, so a command names exactly one
// carrier, and the logging capability taken up optionally.
func TestTheDescriptorDeliversTheSenderExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the sending seam alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*sms.Sender)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want an exclusive claim on sms.Sender", p)
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

// TestPrepareStatesEnabledForARunnableConfiguration holds that a self-consistent
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

// TestPrepareRefusesAnInconsistentTemplate holds the startup gate: a template
// set the carrier cannot send under stops the assembly before anything is
// constructed.
func TestPrepareRefusesAnInconsistentTemplate(t *testing.T) {
	bad := validTemplates()
	bad["authn.code.mfa"] = sms.TemplateSpec{Params: []string{"code"}, Codes: map[string]string{"zh-CN": "  "}}
	body := `{"sms":{"aliyun":{` +
		`"access-key-id":"the-id","access-key-secret":"the-secret","sign-name":"Speed",` +
		`"templates":` + encodeTemplates(bad) + `}}}`
	reg := registryWithReader(t, body)
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("Prepare gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheModuleDeliversTheSenderThroughTheRegistry drives a real registry through
// this module's descriptor to Init, resolves the sending seam from the product and
// names it.
func TestTheModuleDeliversTheSenderThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, completeBody())
	reg.Register(Module())

	var delivered sms.Sender
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			delivered, err = core.Resolve[sms.Sender](reg)
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

// encodeTemplates writes a template map as the JSON a case configures.
func encodeTemplates(templates map[string]sms.TemplateSpec) string {
	b, err := json.Marshal(templateDoc(templates))
	if err != nil {
		panic(err)
	}
	return string(b)
}
