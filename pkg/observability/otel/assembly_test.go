package otel_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

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
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedConfig{serviceName: "orders"}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(buf, nil))}, log.ModuleName)
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

// TestTheAssemblerStartsOnItsDefaultsWithNoEntryModule is the shape a
// background process has: this module is taken in on its default configuration,
// whose endpoint list asks for every endpoint the entry module declares, and
// the assembly holds no entry module. Nothing in that configuration asked for a
// particular endpoint, so the missing entry module is not a failure.
func TestTheAssemblerStartsOnItsDefaultsWithNoEntryModule(t *testing.T) {
	if _, _, err := assemble(t, otel.ModuleName); err != nil {
		t.Fatalf("a process with no entry module could not start this module: %v", err)
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
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedConfig{serviceName: "orders"}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(secondProvider{}.Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.other-assembler"},
	})
	if err == nil {
		t.Fatal("two exclusive providers of the tracer provider started together")
	}
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Errorf("the failure is %v, want the exclusive-delivery violation: a startup that failed over some "+
			"other premise would name the other provider too", err)
	}
	if !strings.Contains(err.Error(), "observability.other-assembler") {
		t.Errorf("the failure is %v, want it to name the other provider", err)
	}
}

// suppliedProviders is an instance a host supplies for the assembler: it
// delivers both provider capabilities and is not this module's own product. It
// counts each Shutdown it is asked for into shutdowns, which is how a case sees
// whether the assembler's stop reached it.
type suppliedProviders struct {
	tracenoop.TracerProvider
	metricnoop.MeterProvider
	shutdowns *int
}

func (p suppliedProviders) Shutdown(context.Context) error {
	*p.shutdowns++
	return nil
}

// namingConfig is a reading face whose configuration names an endpoint, which
// is what makes the assembler reach for an entry module.
type namingConfig struct{}

func (namingConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		cfg := target.(*otel.Config)
		cfg.ServiceName = "orders"
		cfg.Endpoints = []string{"api"}
	}
	return nil
}

// TestAnInstanceSuppliedForTheAssemblerIsLeftAlone pins what the assembler's
// callbacks do with an instance that is not its own product. The configuration
// names an endpoint while the registry holds no entry module, so a wiring stage
// that acted on the supplied instance would fail the startup for want of one.
// Nothing is registered, the stop that follows leaves the instance as it is,
// and what a consumer resolves is the instance the host supplied. The module
// writes no line: its New is the one that does, and New does not run for a
// supplied instance.
func TestAnInstanceSuppliedForTheAssemblerIsLeftAlone(t *testing.T) {
	logged := &bytes.Buffer{}
	shutdowns := 0
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(namingConfig{}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(logged, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Supply(suppliedProviders{shutdowns: &shutdowns}, otel.ModuleName)

	var resolved observability.TracerProvider
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			resolved, err = core.Resolve[observability.TracerProvider](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("an instance supplied for the assembler failed the drive: %v", err)
	}
	if _, ok := resolved.(suppliedProviders); !ok {
		t.Errorf("the tracer provider is %T, want the instance the host supplied", resolved)
	}
	if shutdowns != 0 {
		t.Errorf("the stop asked the supplied instance to shut down %d time(s), want none: what it "+
			"holds is released by the host that supplied it", shutdowns)
	}
	if logged.Len() != 0 {
		t.Errorf("the module logged %q, want nothing: it is New that writes lines, and New does not run "+
			"for an instance the host supplied", logged.String())
	}
}
