package sms

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
)

// pickConfig is the empty configuration the selection cases' carriers accept.
type pickConfig struct{}

// pickProduct is a carrier that answers both seams and does nothing.
type pickProduct struct{}

func (pickProduct) Name() string { return "pick" }

func (pickProduct) Send(context.Context, Resolved) (Receipt, error) { return Receipt{}, nil }

func (pickProduct) Model() CarrierModel { return CarrierModel{} }

func (pickProduct) Offer(context.Context, Submission) (string, error) { return "", nil }

func (pickProduct) Approval(context.Context, string) (Approval, error) { return Approval{}, nil }

func (pickProduct) Retire(context.Context, string) error { return nil }

// pickModule builds a carrier whose stance the case controls.
func pickModule(moduleName, carrierName string, fallback bool, stance func(*pickConfig) (core.Enablement, error)) core.Module {
	prototype := pickConfig{}
	return NewModule(Spec[pickConfig]{
		ModuleName:      moduleName,
		CarrierName:     carrierName,
		Fallback:        fallback,
		ConfigNamespace: moduleName,
		Config:          &prototype,
		Stance:          stance,
		Open: func(context.Context, *pickConfig) (Product, error) {
			return pickProduct{}, nil
		},
	})
}

// configured is the stance of a carrier that is ready to run.
func configured(*pickConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

// pickRegistry builds a registry with a real configuration reader over body and
// the given carrier modules registered.
func pickRegistry(t *testing.T, body string, modules ...core.Module) *core.Registry {
	t.Helper()
	declared := append([]core.Module{{
		Name:      "selection.test",
		Resources: []any{SelectionSchema()},
	}}, modules...)
	reader := configtest.Reader(t, body, declared...)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	for _, module := range declared {
		reg.Register(module)
	}
	return reg
}

// TestSelectionRefusesAnUnknownCarrierName holds the compiled-name check: a name
// no carrier answers to is a startup error naming the value and the capability.
func TestSelectionRefusesAnUnknownCarrierName(t *testing.T) {
	aliyun := pickModule("sms.pick.aliyun", "aliyun", false, configured)
	reg := pickRegistry(t, `{"sms":{"carrier":"carrier-pigeon"}}`, aliyun)

	_, err := aliyun.Prepare(t.Context(), reg)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("an unknown carrier name gave %v, want one wrapping ErrInvalidConfig", err)
	}
	for _, want := range []string{"carrier-pigeon", "sms.carrier", "sms", "aliyun"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err.Error(), want)
		}
	}
}

// TestTwoEnabledCarriersFailNamingBothModules holds the exclusive claim: with no
// selection and two configured carriers, resolution refuses the run and names
// both modules.
func TestTwoEnabledCarriersFailNamingBothModules(t *testing.T) {
	first := pickModule("sms.pick.one", "one", false, configured)
	second := pickModule("sms.pick.two", "two", false, configured)
	reg := pickRegistry(t, `{}`, first, second)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"sms.pick.one", "sms.pick.two", config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			_, err := core.Resolve[Sender](reg)
			return err
		},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("two enabled carriers gave %v, want one wrapping ErrExclusiveViolated", err)
	}
	for _, name := range []string{"sms.pick.one", "sms.pick.two"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the failure %q does not name %q", err.Error(), name)
		}
	}
}

// TestSelectionNoneStandsEveryCarrierDown holds the explicit off value: none
// switches the capability off, so the carriers stand down rather than fall back
// to the log channel.
func TestSelectionNoneStandsEveryCarrierDown(t *testing.T) {
	primary := pickModule("sms.pick.primary", "primary", false, configured)
	fallback := pickModule("sms.pick.fallback", "log", true, configured)
	reg := pickRegistry(t, `{"sms":{"carrier":"none"}}`, primary, fallback)

	for _, module := range []core.Module{primary, fallback} {
		got, err := module.Prepare(t.Context(), reg)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", module.Name, err)
		}
		if got.State != core.StateDisabled || !strings.Contains(got.Reason, "none") {
			t.Errorf("%s: Prepare = %+v, want disabled naming none", module.Name, got)
		}
	}
}
