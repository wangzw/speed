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

// seams satisfies the permission read without implementing it: Init resolves
// it and stores it, and no method is called before a request arrives.
type seams struct{ authz.SubjectPermissions }

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the fragment
// to the route the module's own Init registers.
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

// recordingGate records every open declaration.
type recordingGate struct {
	opened [][2]string
}

func (*recordingGate) Require(string, string, authz.Permission) {}
func (g *recordingGate) Open(method, path string) {
	g.opened = append(g.opened, [2]string{method, path})
}
func (*recordingGate) Covered(string, string) bool { return false }

// TestWireDeclaresTheRouteOpen holds the wiring: the module takes the
// route-declaration surface up in Init and records the route as needing no
// permission, so the enforcement middleware lets an authenticated request
// through to the handler.
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

// driveInit brings the module to Init with the given stand-in modules and
// configuration body, and returns the drive's error. The configuration module
// and the reader are added here, so each case names only what it varies.
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

// TestNoEntryPointIsErrNoRouter holds the first start failure: a command that
// mounts the permission read and names no entry point has nothing to mount it
// on, and the failure names this module so the fix -- writing the entry point
// module's name into the command -- is visible.
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

// TestAnUnknownEndpointIsErrUnknownEndpoint holds the second start failure: a
// name the entry point does not declare stops the startup naming that
// endpoint, before any route is registered.
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
