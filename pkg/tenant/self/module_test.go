package self

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

type quietLogging struct{}

func (quietLogging) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (quietLogging) Redaction() log.Redaction  { return quietRedaction{} }

type quietRedaction struct{}

func (quietRedaction) AddKeys(...string)              {}
func (quietRedaction) AddPattern(string, log.Matcher) {}

type membershipsSeam struct{ authn.Memberships }

type catalogSeam struct{ tenant.Catalog }

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the fragment's route
// is the one Init registers.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"account"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.memberships", &membershipsSeam{}, (*authn.Memberships)(nil)),
			routecontract.StandIn("stand-in.catalog", &catalogSeam{}, (*tenant.Catalog)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"account"}},
		},
	})
}

type recordingGate struct {
	opened [][2]string
}

func (*recordingGate) Require(string, string, authz.Permission) {}
func (g *recordingGate) Open(method, path string) {
	g.opened = append(g.opened, [2]string{method, path})
}
func (*recordingGate) Covered(string, string) bool { return false }

type recordingAdmission struct {
	authn.Gate
	impersonable [][3]string
}

func (a *recordingAdmission) Impersonable(endpoint, method, path string) {
	a.impersonable = append(a.impersonable, [3]string{endpoint, method, path})
}

// TestWireRegistersTheRouteImpersonable checks that Init registers the route as impersonable
// on the admission surface.
func TestWireRegistersTheRouteImpersonable(t *testing.T) {
	admission := &recordingAdmission{}
	router := routecontract.NewRouter("account")
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.memberships", &membershipsSeam{}, (*authn.Memberships)(nil)),
		routecontract.StandIn("stand-in.catalog", &catalogSeam{}, (*tenant.Catalog)(nil)),
		routecontract.StandIn("stand-in.admission", admission, (*authn.Gate)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
		router.Module(),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["account"]}}`
	if err := driveInit(t, body, modules...); err != nil {
		t.Fatalf("driving the module to Init: %v", err)
	}

	want := [3]string{"account", http.MethodGet, pathTenants}
	if !slices.Contains(admission.impersonable, want) {
		t.Errorf("the admission surface records %v, want %s %s on account registered Impersonable",
			admission.impersonable, http.MethodGet, pathTenants)
	}
}

// TestWireDeclaresTheRouteOpen checks that Init declares the route open on the route-
// declaration surface.
func TestWireDeclaresTheRouteOpen(t *testing.T) {
	gate := &recordingGate{}
	router := routecontract.NewRouter("account")
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.memberships", &membershipsSeam{}, (*authn.Memberships)(nil)),
		routecontract.StandIn("stand-in.catalog", &catalogSeam{}, (*tenant.Catalog)(nil)),
		routecontract.StandIn("stand-in.gate", gate, (*authz.Gate)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
		router.Module(),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["account"]}}`
	reader := configtest.Reader(t, body, modules...)

	reg := core.New()
	reg.Register(config.Module())
	names := []string{config.ModuleName}
	for _, module := range modules {
		reg.Register(module)
		names = append(names, module.Name)
	}
	reg.Supply(reader, config.ModuleName)
	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: names}); err != nil {
		t.Fatalf("driving the module to Init: %v", err)
	}

	if !slices.Contains(gate.opened, [2]string{http.MethodGet, pathTenants}) {
		t.Errorf("the gate records %v, want %s %s declared open",
			gate.opened, http.MethodGet, pathTenants)
	}
}

func driveInit(t *testing.T, body string, modules ...core.Module) error {
	t.Helper()
	reader := configtest.Reader(t, body, modules...)

	reg := core.New()
	reg.Register(config.Module())
	names := []string{config.ModuleName}
	for _, module := range modules {
		reg.Register(module)
		names = append(names, module.Name)
	}
	reg.Supply(reader, config.ModuleName)
	return reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: names})
}

// TestNoEntryPointIsErrNoRouter checks that a command with no entry point fails with
// tenant.ErrNoRouter naming this module.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.memberships", &membershipsSeam{}, (*authn.Memberships)(nil)),
		routecontract.StandIn("stand-in.catalog", &catalogSeam{}, (*tenant.Catalog)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["account"]}}`

	err := driveInit(t, body, modules...)
	if !errors.Is(err, tenant.ErrNoRouter) {
		t.Fatalf("driving the module without an entry point ended with %v, want tenant.ErrNoRouter", err)
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// TestAnUnknownEndpointIsErrUnknownEndpoint checks that an endpoint the entry point does not
// declare fails startup with tenant.ErrUnknownEndpoint before any route is registered.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	router := routecontract.NewRouter("account")
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.memberships", &membershipsSeam{}, (*authn.Memberships)(nil)),
		routecontract.StandIn("stand-in.catalog", &catalogSeam{}, (*tenant.Catalog)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
		router.Module(),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["missing"]}}`

	err := driveInit(t, body, modules...)
	if !errors.Is(err, tenant.ErrUnknownEndpoint) {
		t.Fatalf("driving the module to an undeclared endpoint ended with %v, want tenant.ErrUnknownEndpoint", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}
