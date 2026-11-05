package personal

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// The stand-in types embed the capability's interface, so a module that only
// resolves and holds one is satisfied without implementing a method.
type (
	catalogStub       struct{ tenant.Catalog }
	txCatalogStub     struct{ tenantcatalog.TxCatalog }
	txManagerStub     struct{ authzpolicy.TxManager }
	managerStub       struct{ authz.Manager }
	seederStub        struct{ authz.Seeder }
	authzCatalogStub  struct{ authz.Catalog }
	auditStub         struct{ audit.Audit }
	membershipsStub   struct{ authn.Memberships }
	cleanupStub       struct{ authn.MembershipCleanup }
	writerStub        struct{ provisioning.MembershipWriter }
	admissionRecorder struct {
		authn.Gate
		impersonable [][3]string
	}
)

func (a *admissionRecorder) Impersonable(endpoint, method, path string) {
	a.impersonable = append(a.impersonable, [3]string{endpoint, method, path})
}

// standIns is every capability this module resolves.
func standIns(admission *admissionRecorder) []core.Module {
	modules := []core.Module{
		routecontract.StandIn("stand-in.catalog", catalogStub{}, (*tenant.Catalog)(nil)),
		routecontract.StandIn("stand-in.tx-catalog", txCatalogStub{}, (*tenantcatalog.TxCatalog)(nil)),
		routecontract.StandIn("stand-in.tx-manager", txManagerStub{}, (*authzpolicy.TxManager)(nil)),
		routecontract.StandIn("stand-in.manager", managerStub{}, (*authz.Manager)(nil)),
		routecontract.StandIn("stand-in.seeder", seederStub{}, (*authz.Seeder)(nil)),
		routecontract.StandIn("stand-in.authz-catalog", authzCatalogStub{}, (*authz.Catalog)(nil)),
		routecontract.StandIn("stand-in.audit", auditStub{}, (*audit.Audit)(nil)),
		routecontract.StandIn("stand-in.memberships", membershipsStub{}, (*authn.Memberships)(nil)),
		routecontract.StandIn("stand-in.cleanup", cleanupStub{}, (*authn.MembershipCleanup)(nil)),
		routecontract.StandIn("stand-in.writer", writerStub{}, (*provisioning.MembershipWriter)(nil)),
	}
	if admission != nil {
		modules = append(modules, routecontract.StandIn("stand-in.admission", admission, (*authn.Gate)(nil)))
	}
	return modules
}

// The descriptor is the module the registry knows, and the feature is static:
// there is no configuration item that withdraws it.
func TestTheDescriptorIsStaticAndDeliversTheThreeCapabilities(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module name is %q, want %q", m.Name, ModuleName)
	}
	want := map[core.Token]bool{
		(*authn.AccountLifecycle)(nil):           false,
		(*provisioning.AccountProvisioning)(nil): false,
		(*tenant.KindResolver)(nil):              false,
	}
	for _, provision := range m.Provides {
		if _, ok := want[provision.Token]; ok {
			want[provision.Token] = true
		}
	}
	for token, found := range want {
		if !found {
			t.Errorf("the module does not provide %v", token)
		}
	}
	var static bool
	for _, resource := range m.Resources {
		switch value := resource.(type) {
		case feature.Spec:
			if value.Name != FeatureName || value.Conditional {
				t.Errorf("the feature is %+v, want %q static", value, FeatureName)
			}
			static = true
		case config.Schema:
			t.Errorf("the module declares configuration %q; it is always in force", value.Namespace)
		}
	}
	if !static {
		t.Error("the module declares no static feature")
	}
}

// The fragment's declared operation is the route the module registers, and the
// route is the impersonable one the backfill needs.
func TestTheDeclaredOperationIsTheRouteTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"account"},
		Stubs:     standIns(nil),
	})
}

// The route is registered for an impersonating identity: the ensure acts for
// the effective account, so an impersonator builds or reads back the
// impersonated account's own personal tenant.
func TestWireRegistersTheRouteImpersonable(t *testing.T) {
	admission := &admissionRecorder{}
	router := routecontract.NewRouter("account")
	modules := append([]core.Module{Module()}, standIns(admission)...)
	modules = append(modules, router.Module(), quietLoggingModule())
	reader := configtest.Reader(t, "{}", modules...)

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

	want := [3]string{"account", http.MethodPost, RoutePath}
	found := false
	for _, got := range admission.impersonable {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the admission surface records %v (routes %v), want %s %s registered Impersonable",
			admission.impersonable, router.Routes(), http.MethodPost, RoutePath)
	}
}

// quietLoggingModule delivers a logger that discards every record.
func quietLoggingModule() core.Module {
	return routecontract.StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil))
}

type quietLogging struct{}

func (quietLogging) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (quietLogging) Redaction() log.Redaction  { return quietRedaction{} }

type quietRedaction struct{}

func (quietRedaction) AddKeys(...string)              {}
func (quietRedaction) AddPattern(string, log.Matcher) {}
