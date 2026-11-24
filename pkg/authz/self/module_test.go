package self

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/log"
)

type seams struct{ authz.SubjectPermissions }

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the declared operations are the routes the module registers.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"api"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.permissions", &seams{}, (*authz.SubjectPermissions)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"api"}},
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

// TestWireDeclaresTheRouteOpen checks that wire declares the route open.
func TestWireDeclaresTheRouteOpen(t *testing.T) {
	gate := &recordingGate{}
	router := routecontract.NewRouter("api")
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.permissions", &seams{}, (*authz.SubjectPermissions)(nil)),
		routecontract.StandIn("stand-in.gate", gate, (*authz.Gate)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
		router.Module(),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["api"]}}`
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

	if !slices.Contains(gate.opened, [2]string{http.MethodGet, pathPermissions}) {
		t.Errorf("the gate records %v, want %s %s declared open",
			gate.opened, http.MethodGet, pathPermissions)
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

// TestNoEntryPointIsErrNoRouter checks that no entry point fails with ErrNoRouter.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.permissions", &seams{}, (*authz.SubjectPermissions)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["api"]}}`

	err := driveInit(t, body, modules...)
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("driving the module without an entry point ended with %v, want authz.ErrNoRouter", err)
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// TestAnUnknownEndpointIsErrUnknownEndpoint checks that an unknown endpoint fails with ErrUnknownEndpoint.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	router := routecontract.NewRouter("api")
	modules := []core.Module{
		Module(),
		routecontract.StandIn("stand-in.permissions", &seams{}, (*authz.SubjectPermissions)(nil)),
		routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil)),
		router.Module(),
	}
	body := `{"` + ConfigNamespace + `":{"endpoints":["missing"]}}`

	err := driveInit(t, body, modules...)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("driving the module to an undeclared endpoint ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}
