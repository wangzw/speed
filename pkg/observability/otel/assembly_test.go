package otel_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/otel"
)

// fixedConfig is a reading face that gives this module a service name and
// nothing else, so every other item keeps the default its prototype carries.
// It stands in for the real loader, which cannot be driven without a config
// file and a command line.
type fixedConfig struct{ serviceName string }

func (f fixedConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		target.(*otel.Config).ServiceName = f.serviceName
	}
	return nil
}

// stubLogger is the logging capability, reduced to what this module uses of it.
type stubLogger struct{ lg *slog.Logger }

func (s stubLogger) Named(string) *slog.Logger { return s.lg }
func (s stubLogger) Redaction() log.Redaction  { return nil }

// assemble drives a registry holding this module to the wiring stage and hands
// back what the module produced.
func assemble(t *testing.T, participants ...string) (*core.Registry, *bytes.Buffer, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	reg := core.New()
	reg.Supply((*config.Reader)(nil), fixedConfig{serviceName: "orders"})
	reg.Supply((*log.Logger)(nil), stubLogger{lg: slog.New(slog.NewTextHandler(buf, nil))})
	reg.Register(otel.Module())
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
	})
	return reg, buf, err
}

// TestTheAssemblerDeliversBothProvidersFromOneProduct pins the delivery
// contract: the descriptor promises two capabilities, the registry requires one
// product to satisfy both, and a consumer resolving either gets something it
// can build spans and instruments on.
func TestTheAssemblerDeliversBothProvidersFromOneProduct(t *testing.T) {
	reg, _, err := assemble(t, otel.ModuleName)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	got, ok := reg.Enablement(otel.ModuleName)
	if !ok {
		t.Fatalf("the registry has no verdict for %s", otel.ModuleName)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("%s is %v, want enabled", otel.ModuleName, got.State)
	}

	tp, err := core.Resolve[observability.TracerProvider](reg)
	if err != nil {
		t.Fatalf("resolving the tracer provider: %v", err)
	}
	if tp == nil {
		t.Fatal("the tracer provider is nil")
	}
	if tp.Tracer("test") == nil {
		t.Error("the tracer provider hands out no tracer")
	}
	mp, err := core.Resolve[observability.MeterProvider](reg)
	if err != nil {
		t.Fatalf("resolving the meter provider: %v", err)
	}
	if mp == nil {
		t.Fatal("the meter provider is nil")
	}
	if mp.Meter("test") == nil {
		t.Error("the meter provider hands out no meter")
	}
}

// TestTheAssemblerStartsWithNoBackendAndSaysSo pins the one observable
// difference between a module that produces nothing and a module that is
// broken. Production without a destination is a legal configuration, so it must
// not fail; the line is the only thing that stops it from looking like a fault.
func TestTheAssemblerStartsWithNoBackendAndSaysSo(t *testing.T) {
	_, buf, err := assemble(t, otel.ModuleName)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if !strings.Contains(buf.String(), "no export backend") {
		t.Errorf("the log says %q, want a line reporting that nothing is exported to: without it, an "+
			"assembly that produces nothing is indistinguishable from one that is broken", buf.String())
	}
}

// TestTheAssemblerTouchesNoEntryModuleAndNoBusWhenNeitherIsNamed is the shape a
// background process has: this module is taken in, no listening endpoint and no
// event type is configured, and neither capability is registered in the
// assembly at all. Reaching for either would fail the startup of a process that
// legitimately has neither.
func TestTheAssemblerTouchesNoEntryModuleAndNoBusWhenNeitherIsNamed(t *testing.T) {
	if _, _, err := assemble(t, otel.ModuleName); err != nil {
		t.Fatalf("a process with no entry module and no bus could not start this module: %v", err)
	}
}

// secondProvider is a module that contests the tracer provider, so that the
// exclusive claim this module makes has something to be violated by.
type secondProvider struct{}

func (secondProvider) Module() core.Module {
	return core.Module{
		Name:     "observability.other-assembler",
		Provides: []core.Provision{{Token: (*observability.TracerProvider)(nil), Exclusive: true}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
	}
}

// TestASecondAssemblerFailsTheStartupRatherThanSplittingTheSignals pins why the
// delivery is exclusive. Two assemblies would each hand out their own providers,
// so a consumer's spans would go to one pipeline and its metrics to another,
// with nothing reporting the split — the failure has to be named at startup
// instead.
func TestASecondAssemblerFailsTheStartupRatherThanSplittingTheSignals(t *testing.T) {
	reg := core.New()
	reg.Supply((*config.Reader)(nil), fixedConfig{serviceName: "orders"})
	reg.Supply((*log.Logger)(nil), stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	reg.Register(otel.Module())
	reg.Register(secondProvider{}.Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.other-assembler"},
	})
	if err == nil {
		t.Fatal("two exclusive providers of the tracer provider started together")
	}
	if !strings.Contains(err.Error(), "observability.other-assembler") {
		t.Errorf("the failure is %v, want it to name the other provider", err)
	}
}
