package selfcreate

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

type (
	catalogStub       struct{ tenant.Catalog }
	membershipsStub   struct{ authn.Memberships }
	verificationStub  struct{ authn.SignupVerification }
	reservationStub   struct{ authn.UsernameReservation }
	provisionerStub   struct{ tenant.Provisioner }
	kvStub            struct{ kv.KV }
	settingsStub      struct{ settings.Store }
	auditStub         struct{ audit.Audit }
	admissionRecorder struct {
		authn.Gate
		impersonable [][3]string
	}
	featureRecorder struct {
		feature.Board
		posts [][2]string
	}
)

func (a *admissionRecorder) Impersonable(endpoint, method, path string) {
	a.impersonable = append(a.impersonable, [3]string{endpoint, method, path})
}

func (f *featureRecorder) Post(module, name string) {
	f.posts = append(f.posts, [2]string{module, name})
}

func standIns(admission *admissionRecorder, board *featureRecorder) []core.Module {
	modules := []core.Module{
		routecontract.StandIn("stand-in.catalog", catalogStub{}, (*tenant.Catalog)(nil)),
		routecontract.StandIn("stand-in.memberships", membershipsStub{}, (*authn.Memberships)(nil)),
		routecontract.StandIn("stand-in.verification", verificationStub{}, (*authn.SignupVerification)(nil)),
		routecontract.StandIn("stand-in.reservation", reservationStub{}, (*authn.UsernameReservation)(nil)),
		routecontract.StandIn("stand-in.provisioner", provisionerStub{}, (*tenant.Provisioner)(nil)),
		routecontract.StandIn("stand-in.kv", kvStub{}, (*kv.KV)(nil)),
		routecontract.StandIn("stand-in.settings", settingsStub{}, (*settings.Store)(nil)),
		routecontract.StandIn("stand-in.audit", auditStub{}, (*audit.Audit)(nil)),
	}
	if admission != nil {
		modules = append(modules, routecontract.StandIn("stand-in.admission", admission, (*authn.Gate)(nil)))
	}
	if board != nil {
		modules = append(modules, routecontract.StandIn("stand-in.board", board, (*feature.Board)(nil)))
	}
	return modules
}

func drive(t *testing.T, document string) (*routecontract.Router, *featureRecorder) {
	t.Helper()
	router := routecontract.NewRouter(endpointName)
	admission := &admissionRecorder{}
	board := &featureRecorder{}
	modules := append([]core.Module{Module()}, standIns(admission, board)...)
	modules = append(modules, router.Module())
	reader := configtest.Reader(t, document, modules...)

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
	return router, board
}

// TestTheDescriptorDeclaresTheConditionalFeature checks the module's name, its
// conditional feature and the route's endpoint.
func TestTheDescriptorDeclaresTheConditionalFeature(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "org.create" {
		t.Errorf("the module is named %q, want org.create", m.Name)
	}
	var found bool
	for _, resource := range m.Resources {
		if value, ok := resource.(feature.Spec); ok {
			if value.Name != FeatureName || !value.Conditional {
				t.Errorf("the feature is %+v, want %q conditional", value, FeatureName)
			}
			found = true
		}
	}
	if !found {
		t.Error("the module declares no feature")
	}
}

// TestTheSettingsSchemaIsPlatformOnly checks that no item is tenant-facing and
// the defaults are the product's two bounds.
func TestTheSettingsSchemaIsPlatformOnly(t *testing.T) {
	schema := settingsSchema()
	if schema.Namespace != SettingsNamespace || SettingsNamespace != "org.create" {
		t.Errorf("the settings namespace is %q, want org.create", schema.Namespace)
	}
	if schema.TenantFacing {
		t.Error("the settings schema declares itself tenant-facing")
	}
	if len(schema.Items) != 2 {
		t.Fatalf("the schema declares %d items, want two", len(schema.Items))
	}
	for name, item := range schema.Items {
		if item.TenantAccess != settings.TenantNone || item.Control != settings.ControlNone ||
			item.Danger != settings.DangerNone || item.Effect != settings.EffectNone {
			t.Errorf("item %q carries tenant presentation: %+v", name, item)
		}
		if item.Domain.Rule() != "the positive integers" {
			t.Errorf("item %q domain is %q, want the positive integers", name, item.Domain.Rule())
		}
	}
	if len(schema.Mounts) != 1 {
		t.Fatalf("the schema carries %d mounts, want one", len(schema.Mounts))
	}
	defaults, ok := schema.Mounts[0].Value.(*limitDefaults)
	if !ok {
		t.Fatalf("the mount carries %T, want *limitDefaults", schema.Mounts[0].Value)
	}
	if defaults.MaxOrganizations != 5 || defaults.MaxPerHour != 3 {
		t.Errorf("the defaults are %+v, want five organizations and three an hour", defaults)
	}
}

// TestTheDeclaredOperationIsTheRouteTheModuleRegisters checks the declared
// operation against the route registered on the account endpoint.
func TestTheDeclaredOperationIsTheRouteTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{endpointName},
		Stubs:     standIns(nil, nil),
		Config:    map[string]any{"org": map[string]any{"create": map[string]any{"enabled": true}}},
	})
}

// With the switch on, the route is on the account endpoint, registered impersonable,
// and the feature is posted.
func TestTheSwitchDecidesTheRouteAndTheFeature(t *testing.T) {
	on, board := drive(t, `{"org":{"create":{"enabled":true}}}`)
	if got := on.Routes(); len(got) != 1 || got[0] != (routecontract.Route{Method: http.MethodPost, Path: RoutePath}) {
		t.Errorf("the on switch mounted %v, want POST %s", got, RoutePath)
	}
	if len(board.posts) != 1 || board.posts[0] != [2]string{ModuleName, FeatureName} {
		t.Errorf("the on switch posted %v, want %q on %q", board.posts, FeatureName, ModuleName)
	}

	off, board := drive(t, `{"org":{"create":{"enabled":false}}}`)
	if got := off.Routes(); len(got) != 0 {
		t.Errorf("the off switch mounted %v, want nothing", got)
	}
	if len(board.posts) != 0 {
		t.Errorf("the off switch posted %v, want nothing", board.posts)
	}
}

// TestTheRoutePathIsTheAccountEndpointsOrgsPath checks the fragment's path
// against the absolute route the module mounts.
func TestTheRoutePathIsTheAccountEndpointsOrgsPath(t *testing.T) {
	if RoutePath != "/api/v1/orgs" {
		t.Errorf("RoutePath = %q, want /api/v1/orgs", RoutePath)
	}
}

// TestTheOperationIsOneExactPost checks that the route is one exact POST, not a
// prefix over the personal tenant's path.
func TestTheOperationIsOneExactPost(t *testing.T) {
	if len(operations) != 1 {
		t.Fatalf("the module declares %d operations, want one", len(operations))
	}
	if createOperation.Method != http.MethodPost {
		t.Errorf("the method is %q, want POST", createOperation.Method)
	}
	if createOperation.Path != pathFragment {
		t.Errorf("the path is %q, want %q", createOperation.Path, pathFragment)
	}
	if createOperation.ID != "org.create.create" {
		t.Errorf("the operation id is %q, want org.create.create", createOperation.ID)
	}
	if reflect.ValueOf(createOperation).IsZero() {
		t.Error("the operation is the zero value")
	}
}
