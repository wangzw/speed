package mail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
)

type pickConfig struct{}

type pickSender struct{}

func (pickSender) Name() string { return "pick" }

func (pickSender) Send(context.Context, Message) (Receipt, error) { return Receipt{}, nil }

func pickModule(moduleName, carrierName string, fallback bool, stance func(*pickConfig) (core.Enablement, error)) core.Module {
	prototype := pickConfig{}
	return NewModule(Spec[pickConfig]{
		ModuleName:      moduleName,
		CarrierName:     carrierName,
		Fallback:        fallback,
		ConfigNamespace: moduleName,
		Config:          &prototype,
		Stance:          stance,
		Open: func(context.Context, *pickConfig) (Sender, error) {
			return pickSender{}, nil
		},
	})
}

func configured(*pickConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

func unconfigured(*pickConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateDisabled, Reason: "the section is unset"}, nil
}

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

// TestSelectionRefusesAnUnknownCarrierName checks that a name no compiled carrier
// answers to is a startup error naming the value and the compiled carriers.
func TestSelectionRefusesAnUnknownCarrierName(t *testing.T) {
	smtp := pickModule("mail.pick.smtp", "smtp", false, configured)
	reg := pickRegistry(t, `{"mail":{"carrier":"ses"}}`, smtp)

	_, err := smtp.Prepare(t.Context(), reg)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("an unknown carrier name gave %v, want one wrapping ErrInvalidConfig", err)
	}
	for _, want := range []string{"ses", "mail.carrier", "mail"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err.Error(), want)
		}
	}
	if !strings.Contains(err.Error(), "smtp") {
		t.Errorf("the failure %q does not name the compiled carrier", err.Error())
	}
}

// TestTwoEnabledCarriersFailNamingBothModules checks that two configured carriers
// with no selection fail resolution naming both modules.
func TestTwoEnabledCarriersFailNamingBothModules(t *testing.T) {
	first := pickModule("mail.pick.one", "one", false, configured)
	second := pickModule("mail.pick.two", "two", false, configured)
	reg := pickRegistry(t, `{}`, first, second)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"mail.pick.one", "mail.pick.two", config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			_, err := core.Resolve[Sender](reg)
			return err
		},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("two enabled carriers gave %v, want one wrapping ErrExclusiveViolated", err)
	}
	for _, name := range []string{"mail.pick.one", "mail.pick.two"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the failure %q does not name %q", err.Error(), name)
		}
	}
}

// TestSelectionNoneStandsEveryCarrierDown checks that the none selection stands
// every carrier down, the fallback included.
func TestSelectionNoneStandsEveryCarrierDown(t *testing.T) {
	primary := pickModule("mail.pick.primary", "primary", false, configured)
	fallback := pickModule("mail.pick.fallback", "log", true, configured)
	reg := pickRegistry(t, `{"mail":{"carrier":"none"}}`, primary, fallback)

	for _, module := range []core.Module{primary, fallback} {
		got, err := module.Prepare(t.Context(), reg)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", module.Name, err)
		}
		if got.State != core.StateDisabled {
			t.Errorf("%s: Prepare = %+v, want disabled", module.Name, got)
		}
		if !strings.Contains(got.Reason, "none") {
			t.Errorf("%s: reason %q does not say the selection switched the capability off",
				module.Name, got.Reason)
		}
	}
}

// TestExplicitSelectionStandsTheOtherCarrierDown checks that a carrier the
// selection does not name stands down without its own configuration being judged.
func TestExplicitSelectionStandsTheOtherCarrierDown(t *testing.T) {
	primary := pickModule("mail.pick.primary", "primary", false, configured)
	fallback := pickModule("mail.pick.fallback", "log", true, configured)
	reg := pickRegistry(t, `{"mail":{"carrier":"primary"}}`, primary, fallback)

	got, err := primary.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the selected carrier: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Errorf("the selected carrier = %+v, want enabled", got)
	}
	other, err := fallback.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the unselected carrier: %v", err)
	}
	if other.State != core.StateDisabled || !strings.Contains(other.Reason, "primary") {
		t.Errorf("the unselected carrier = %+v, want disabled naming the selection", other)
	}
}

// TestFallbackStatesAutoWithoutASelection checks that the fallback carrier states
// StateAuto when no selection names a carrier.
func TestFallbackStatesAutoWithoutASelection(t *testing.T) {
	primary := pickModule("mail.pick.primary", "primary", false, unconfigured)
	fallback := pickModule("mail.pick.fallback", "log", true, configured)
	reg := pickRegistry(t, `{}`, primary, fallback)

	got, err := primary.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the unconfigured carrier: %v", err)
	}
	if got.State != core.StateDisabled {
		t.Errorf("the unconfigured carrier = %+v, want disabled", got)
	}
	fallbackStance, err := fallback.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the fallback carrier: %v", err)
	}
	if fallbackStance.State != core.StateAuto {
		t.Errorf("the fallback with no selection = %+v, want StateAuto", fallbackStance)
	}
}
