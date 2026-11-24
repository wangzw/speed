package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

type descRig struct {
	withDB, withCat bool
	endpoints       []string
	router          *stubRouter
}

func (r descRig) body(t *testing.T) string {
	t.Helper()
	section := map[string]any{}
	if r.endpoints != nil {
		section["endpoints"] = r.endpoints
	}
	document := map[string]any{
		"db":           map[string]any{"sqlite": map[string]any{"dsn": filepath.Join(t.TempDir(), "desc.db")}},
		"notification": map[string]any{"inbox": section},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	return string(encoded)
}

func (r descRig) drive(t *testing.T, depth core.Stage, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	reader := authntest.ConfigReaderAll(t, r.body(t), Module(), sqlite.Module(), catalog.Module())

	modules := []core.Module{Module()}
	if r.withDB {
		modules = append(modules, sqlite.Module())
	}
	if r.withCat {
		modules = append(modules, catalog.Module())
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{config.ModuleName}
	seen := map[string]bool{config.ModuleName: true}
	for _, m := range modules {
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		reg.Register(m)
		names = append(names, m.Name)
	}
	if r.router != nil {
		reg.Register(standIn("stand-in.router", r.router, (*speedhttp.Router)(nil)))
		names = append(names, "stand-in.router")
	}
	if action == nil {
		action = func(context.Context, *core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{Depth: depth, Participants: names, Action: action})
}

// This module registers under its name and delivers notification.Inbox, and
// only that, exclusively.
func TestTheDescriptorNamesAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != "notification.inbox" {
		t.Errorf("the module name is %q, want notification.inbox", m.Name)
	}
	if len(m.Provides) != 1 || m.Provides[0].Token != core.Token((*notification.Inbox)(nil)) || !m.Provides[0].Exclusive {
		t.Errorf("Provides = %+v, want notification.Inbox alone, exclusive", m.Provides)
	}
}

// The descriptor declares the database and the roster as hard requirements and
// the bus, the logger and the entry point as optional ones.
func TestTheDescriptorDeclaresItsDependencies(t *testing.T) {
	want := map[core.Token]bool{
		core.Token((*db.Database)(nil)):      false,
		core.Token((*tenant.Catalog)(nil)):   false,
		core.Token((*event.Bus)(nil)):        true,
		core.Token((*log.Logger)(nil)):       true,
		core.Token((*speedhttp.Router)(nil)): true,
	}
	seen := map[core.Token]bool{}
	for _, req := range Module().Requires {
		optional, ok := want[req.Token]
		if !ok {
			t.Errorf("unexpected requirement %v", req.Token)
			continue
		}
		seen[req.Token] = true
		if req.Optional != optional {
			t.Errorf("requirement %v is optional=%v, want %v", req.Token, req.Optional, optional)
		}
	}
	for token := range want {
		if !seen[token] {
			t.Errorf("the token %v is not declared", token)
		}
	}
}

// The descriptor hands the database its table's migrations and the engine its
// message catalog.
func TestTheDescriptorCarriesItsResources(t *testing.T) {
	var migrations, catalogued bool
	for _, res := range Module().Resources {
		switch res.(type) {
		case db.Migrations:
			migrations = true
		case i18n.Catalog:
			catalogued = true
		}
	}
	if !migrations {
		t.Error("the resources carry no db.Migrations")
	}
	if !catalogued {
		t.Error("the resources carry no i18n.Catalog")
	}
}

// A command that leaves the database or the roster out fails resolution with
// ErrMissingProvider, before any instance is built.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, rig := range map[string]descRig{
		"no database": {withCat: true},
		"no roster":   {withDB: true},
	} {
		t.Run(name, func(t *testing.T) {
			var built any
			err := rig.drive(t, core.StageNew, func(_ context.Context, reg *core.Registry) error {
				built, _ = core.Resolve[notification.Inbox](reg)
				return nil
			})
			if !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("the drive ended with %v, want ErrMissingProvider", err)
			}
			if built != nil {
				t.Error("an instance was delivered before resolution failed")
			}
		})
	}
}

// With the entry point present and endpoints named, the inbox mounts its whole
// route set on each.
func TestARealRegistryMountsEveryRoute(t *testing.T) {
	router := newStubRouter("public")
	rig := descRig{withDB: true, withCat: true, router: router, endpoints: []string{"public"}}
	if err := rig.drive(t, core.StageInit, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	want := map[mountedRoute]bool{
		{http.MethodGet, pathMessages}:     true,
		{http.MethodGet, pathUnreadCount}:  true,
		{http.MethodPost, pathReadAll}:     true,
		{http.MethodPost, pathMessageRead}: true,
	}
	got := router.endpoint("public").registered()
	if len(got) != len(want) {
		t.Fatalf("mounted %d routes (%v), want %d", len(got), got, len(want))
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("unexpected route %s %s", r.method, r.path)
		}
	}
}

// With the entry point present, an empty endpoint list fails the startup naming
// the key.
func TestAnEmptyEndpointListWithARouterFailsInit(t *testing.T) {
	router := newStubRouter("public")
	rig := descRig{withDB: true, withCat: true, router: router, endpoints: []string{}}
	err := rig.drive(t, core.StageInit, nil)
	if !errors.Is(err, notification.ErrInvalidConfig) {
		t.Fatalf("the drive ended with %v, want notification.ErrInvalidConfig", err)
	}
	if err == nil || !strings.Contains(err.Error(), endpointsKey) {
		t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
	}
}

// With no entry point the inbox still comes up and mounts nothing; the endpoint
// list is not read.
func TestWithoutAnEntryPointTheInboxStillRuns(t *testing.T) {
	var face any
	rig := descRig{withDB: true, withCat: true}
	if err := rig.drive(t, core.StageStart, func(_ context.Context, reg *core.Registry) error {
		face, _ = core.Resolve[notification.Inbox](reg)
		return nil
	}); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if face == nil {
		t.Error("the inbox was not delivered without an entry point")
	}
}

// The endpoint list refuses a shape it cannot run on, naming the key, when the
// routes are mounted.
func TestTheEndpointListRefusesBadShapes(t *testing.T) {
	cases := map[string][]string{
		"star":       {"*"},
		"empty name": {""},
		"duplicate":  {"public", "public"},
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			rig := descRig{withDB: true, withCat: true, endpoints: endpoints, router: newStubRouter("public")}
			err := rig.drive(t, core.StageInit, nil)
			if !errors.Is(err, notification.ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want notification.ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), endpointsKey) {
				t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
			}
		})
	}
}

// With no entry point, a malformed endpoint list is not read and the inbox
// still comes up: the list is judged only where the routes are mounted.
func TestAMalformedEndpointListIsNotReadWithoutARouter(t *testing.T) {
	rig := descRig{withDB: true, withCat: true, endpoints: []string{"*", ""}}
	if err := rig.drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed with an unused malformed endpoint list: %v", err)
	}
}
